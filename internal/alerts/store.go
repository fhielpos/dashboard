package alerts

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"time"
)

// SeenEntry records what we know about an already-alerted item: the item's
// own UpdatedAt (used to detect real changes vs. re-appearance with no new
// content) and LastSeenAt (used to expire the entry after CacheTTL of
// absence, so a brief drop from a snapshot doesn't grow the store forever).
type SeenEntry struct {
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

// fileStore persists the seen-set as a JSON object of ID -> SeenEntry on disk.
type fileStore struct {
	path string
}

// NewFileStore returns a Store backed by the file at path.
func NewFileStore(path string) *fileStore {
	return &fileStore{path: path}
}

func (s *fileStore) Load() (map[string]SeenEntry, bool, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]SeenEntry{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	var entries map[string]SeenEntry
	if err := json.Unmarshal(data, &entries); err == nil {
		return entries, true, nil
	}

	// Fall back to the legacy array-of-IDs format so upgrading from the old
	// store never wipes the seen-set and floods alerts on the next poll.
	var ids []string
	if err := json.Unmarshal(data, &ids); err == nil {
		now := time.Now()
		entries = make(map[string]SeenEntry, len(ids))
		for _, id := range ids {
			entries[id] = SeenEntry{LastSeenAt: now}
		}
		return entries, true, nil
	}

	// Neither format parses (e.g. a crash truncated a prior write). Treat it
	// like a missing file rather than erroring forever: Process's cold-start
	// path reseeds the baseline without dispatching, and the next Save
	// (now atomic, see below) overwrites the corrupt file.
	slog.Warn("alerts: seen-set file is corrupt, resetting", "path", s.path)
	return map[string]SeenEntry{}, false, nil
}

func (s *fileStore) Save(seen map[string]SeenEntry) error {
	data, err := json.Marshal(seen) // encoding/json sorts map keys, so output is stable for diffs
	if err != nil {
		return err
	}

	// Write to a temp file and rename into place so a crash mid-write can
	// never leave a truncated/corrupt file at s.path.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
