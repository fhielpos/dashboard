package alerts

import (
	"context"
	"log/slog"

	"github.com/franco/dashboard/internal/core"
)

// Store persists the set of item IDs that have already been alerted on.
type Store interface {
	// Load returns the seen-set and whether a prior state existed. On first
	// run existed is false, which triggers cold-start baseline seeding.
	Load() (seen map[string]bool, existed bool, err error)
	Save(seen map[string]bool) error
}

// Notifier detects newly-appeared alertable items and dispatches them.
type Notifier struct {
	cfg      Config
	channels []Channel
	store    Store
	log      *slog.Logger
}

// New builds a Notifier. log may be nil.
func New(cfg Config, channels []Channel, store Store, log *slog.Logger) *Notifier {
	if log == nil {
		log = slog.Default()
	}
	return &Notifier{cfg: cfg, channels: channels, store: store, log: log}
}

// Process diffs the current item snapshot against the seen-set and dispatches
// alerts for items that are newly present. On the very first run it seeds the
// seen-set with everything currently present without dispatching, so the
// existing backlog never floods on startup.
func (n *Notifier) Process(ctx context.Context, items []core.Item) error {
	seen, existed, err := n.store.Load()
	if err != nil {
		return err
	}
	if seen == nil {
		seen = make(map[string]bool)
	}

	// Collect the alertable items present in this snapshot.
	present := make(map[string]Alert)
	for _, item := range items {
		if a, ok := alertable(item, n.cfg); ok {
			present[a.ID] = a
		}
	}

	// Cold start: seed the baseline silently so the backlog never floods.
	if !existed {
		baseline := make(map[string]bool, len(present))
		for id := range present {
			baseline[id] = true
		}
		return n.store.Save(baseline)
	}

	// Dispatch anything alertable that we haven't seen before.
	for id, a := range present {
		if seen[id] {
			continue
		}
		n.dispatch(ctx, a)
		seen[id] = true
	}

	// Prune to what is still present so the seen-set stays bounded.
	next := make(map[string]bool, len(present))
	for id := range present {
		next[id] = true
	}
	return n.store.Save(next)
}

// dispatch sends an alert to every channel, logging (not failing) on error so
// one bad channel never blocks the others or the refresh cycle.
func (n *Notifier) dispatch(ctx context.Context, a Alert) {
	for _, ch := range n.channels {
		if err := ch.Send(ctx, a); err != nil {
			n.log.Error("alert dispatch failed", "channel", ch.Name(), "id", a.ID, "error", err)
		}
	}
}

// alertable maps an item to an Alert if it matches an enabled rule and passes
// the exclusion filters. The bool is false when the item should not alert.
func alertable(item core.Item, cfg Config) (Alert, bool) {
	repo, _ := item.Metadata["repository"].(string)
	author, _ := item.Metadata["author"].(string)

	if contains(cfg.ExcludeAuthors, author) || contains(cfg.ExcludeRepos, repo) {
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
		case cfg.Rules.ReviewRequests && contains(cats, "review_requested"):
			reason = "review_requested"
		case cfg.Rules.AssignedIssues && contains(cats, "assigned"):
			reason = "assigned"
		default:
			return Alert{}, false
		}
	default:
		return Alert{}, false
	}

	return Alert{
		ID:     item.ID,
		Title:  item.Title,
		URL:    item.URL,
		Repo:   repo,
		Author: author,
		Reason: reason,
	}, true
}

func contains(haystack []string, needle string) bool {
	if needle == "" {
		return false
	}
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
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
