package alerts

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/franco/dashboard/internal/core"
)

// DefaultCacheTTL is how long an absent item is remembered before its entry
// expires, when Config.CacheTTL is unset.
const DefaultCacheTTL = 72 * time.Hour

// Store persists the seen-set: which item IDs have already been alerted on,
// what their content looked like at the time, and when they were last seen.
type Store interface {
	// Load returns the seen-set and whether a prior state existed. On first
	// run existed is false, which triggers cold-start baseline seeding.
	Load() (seen map[string]SeenEntry, existed bool, err error)
	Save(seen map[string]SeenEntry) error
}

// Notifier detects newly-appeared alertable items and dispatches them.
type Notifier struct {
	cfg      Config
	channels []Channel
	store    Store
	log      *slog.Logger
	now      func() time.Time
}

// New builds a Notifier. log may be nil.
func New(cfg Config, channels []Channel, store Store, log *slog.Logger) *Notifier {
	if log == nil {
		log = slog.Default()
	}
	return &Notifier{cfg: cfg, channels: channels, store: store, log: log, now: time.Now}
}

// Process diffs the current item snapshot against the seen-set and dispatches
// alerts for items that are new or have genuinely changed since they were
// last alerted (by comparing the item's own UpdatedAt). On the very first run
// it seeds the seen-set with everything currently present without
// dispatching, so the existing backlog never floods on startup.
//
// An item that briefly disappears from a snapshot (e.g. paginated out of the
// GitHub API's unread window, or its read flag flapping) is not treated as
// new when it reappears unchanged — its entry is kept for CacheTTL after it
// was last seen, and only a real content change (or true expiry) re-alerts.
func (n *Notifier) Process(ctx context.Context, items []core.Item) error {
	seen, existed, err := n.store.Load()
	if err != nil {
		return err
	}
	if seen == nil {
		seen = make(map[string]SeenEntry)
	}

	// Collect the alertable items present in this snapshot.
	present := make(map[string]Alert)
	for _, item := range items {
		if a, ok := alertable(item, n.cfg); ok {
			present[a.ID] = a
		}
	}

	now := n.now()

	// Cold start: seed the baseline silently so the backlog never floods.
	if !existed {
		baseline := make(map[string]SeenEntry, len(present))
		for id, a := range present {
			baseline[id] = SeenEntry{UpdatedAt: a.UpdatedAt, LastSeenAt: now}
		}
		return n.store.Save(baseline)
	}

	ttl := n.cacheTTL()
	next := make(map[string]SeenEntry, len(seen))

	// Keep not-yet-expired entries that dropped out of this snapshot, so a
	// reappearance later is compared against what we already alerted on
	// rather than treated as brand new.
	for id, entry := range seen {
		if _, ok := present[id]; ok {
			continue
		}
		if now.Sub(entry.LastSeenAt) < ttl {
			next[id] = entry
		}
	}

	// Dispatch anything new, or anything whose content actually changed since
	// it was last alerted on. Dispatches run concurrently so a slow or
	// unreachable channel (e.g. ntfy) delays this by at most one channel
	// timeout, rather than multiplying across every alert in the batch.
	var wg sync.WaitGroup
	for id, a := range present {
		entry, known := seen[id]
		if !known || a.UpdatedAt.After(entry.UpdatedAt) {
			wg.Add(1)
			go func(a Alert) {
				defer wg.Done()
				n.dispatch(ctx, a)
			}(a)
		}
		next[id] = SeenEntry{UpdatedAt: a.UpdatedAt, LastSeenAt: now}
	}
	wg.Wait()

	return n.store.Save(next)
}

// cacheTTL returns the configured CacheTTL, falling back to DefaultCacheTTL.
func (n *Notifier) cacheTTL() time.Duration {
	if n.cfg.CacheTTL > 0 {
		return n.cfg.CacheTTL
	}
	return DefaultCacheTTL
}

// dispatch sends an alert to every channel concurrently, logging (not
// failing) on error so one bad or slow channel never blocks the others.
func (n *Notifier) dispatch(ctx context.Context, a Alert) {
	var wg sync.WaitGroup
	for _, ch := range n.channels {
		wg.Add(1)
		go func(ch Channel) {
			defer wg.Done()
			if err := ch.Send(ctx, a); err != nil {
				n.log.Error("alert dispatch failed", "channel", ch.Name(), "id", a.ID, "error", err)
			}
		}(ch)
	}
	wg.Wait()
}

// alertable maps an item to an Alert if it matches an enabled rule and passes
// the exclusion filters. The bool is false when the item should not alert.
func alertable(item core.Item, cfg Config) (Alert, bool) {
	repo, _ := item.Metadata["repository"].(string)
	author, _ := item.Metadata["author"].(string)

	// Guard against an empty author/repo (item metadata not set) matching a
	// stray blank entry in the exclude lists (e.g. a trailing comma in config).
	if (author != "" && slices.Contains(cfg.ExcludeAuthors, author)) ||
		(repo != "" && slices.Contains(cfg.ExcludeRepos, repo)) {
		return Alert{}, false
	}

	reason := ""
	switch item.Plugin {
	case "notifications":
		if !cfg.Rules.Notifications {
			return Alert{}, false
		}
		reason, _ = item.Metadata["reason"].(string)
	case "github":
		cats := toStrings(item.Metadata["categories"])
		switch {
		case cfg.Rules.ReviewRequests && slices.Contains(cats, "review_requested"):
			reason = "review_requested"
		case cfg.Rules.AssignedIssues && slices.Contains(cats, "assigned"):
			reason = "assigned"
		default:
			return Alert{}, false
		}
	default:
		return Alert{}, false
	}

	return Alert{
		ID:        item.ID,
		Title:     item.Title,
		URL:       item.URL,
		Repo:      repo,
		Author:    author,
		Reason:    reason,
		UpdatedAt: metadataTime(item.Metadata["updated_at"]),
	}, true
}

// metadataTime coerces a metadata value into a time.Time, accepting both the
// in-process time.Time form and the RFC3339 string form a JSON round-trip
// yields. Returns the zero value if the field is absent or unparseable, which
// makes changed-detection fall back to plain presence-based dedup.
func metadataTime(v interface{}) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

// toStrings coerces a metadata value into a []string, accepting both the
// in-process []string form and the []interface{} form a JSON round-trip yields.
func toStrings(v interface{}) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []interface{}:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
