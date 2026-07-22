package alerts

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
)

// fileStore persists the seen-set as a JSON array of item IDs on disk.
type fileStore struct {
	path string
}

// NewFileStore returns a Store backed by the file at path.
func NewFileStore(path string) *fileStore {
	return &fileStore{path: path}
}

func (s *fileStore) Load() (map[string]bool, bool, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]bool{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		return nil, false, err
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
	}
	return seen, true, nil
}

func (s *fileStore) Save(seen map[string]bool) error {
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids) // stable output for readable diffs

	data, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}
