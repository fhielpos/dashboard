package alerts

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/franco/dashboard/internal/core"
)

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func notifItem(id, title, repo, reason string) core.Item {
	return notifItemAt(id, title, repo, reason, time.Time{})
}

func notifItemAt(id, title, repo, reason string, updatedAt time.Time) core.Item {
	return core.Item{
		ID:     "notification:" + id,
		Plugin: "notifications",
		Type:   "notification",
		Title:  title,
		Metadata: map[string]interface{}{
			"reason":     reason,
			"repository": repo,
			"updated_at": updatedAt,
		},
	}
}

func ghItem(id, title, repo, author string, categories ...string) core.Item {
	return core.Item{
		ID:     "github:" + id,
		Plugin: "github",
		Type:   "pr",
		Title:  title,
		Metadata: map[string]interface{}{
			"author":     author,
			"repository": repo,
			"categories": categories,
		},
	}
}

// botItem is a review-requested PR whose author the github plugin marked as a bot.
func botItem(id, author string) core.Item {
	item := ghItem(id, "chore(deps): bump", "o/r", author, "review_requested")
	item.Metadata["is_bot"] = true
	return item
}

func allRules() Config {
	return Config{Rules: Rules{Notifications: true, ReviewRequests: true, AssignedIssues: true}}
}

// ── fakes ─────────────────────────────────────────────────────────────────────

type fakeStore struct {
	seen    map[string]SeenEntry
	existed bool
	saved   map[string]SeenEntry
}

func (f *fakeStore) Load() (map[string]SeenEntry, bool, error) {
	out := make(map[string]SeenEntry, len(f.seen))
	for k, v := range f.seen {
		out[k] = v
	}
	return out, f.existed, nil
}

func (f *fakeStore) Save(s map[string]SeenEntry) error {
	f.saved = s
	return nil
}

type fakeChannel struct {
	sent []Alert
}

func (f *fakeChannel) Name() string                          { return "fake" }
func (f *fakeChannel) Send(_ context.Context, a Alert) error { f.sent = append(f.sent, a); return nil }

// ── alertable ───────────────────────────────────────────────────────────────

func TestAlertable(t *testing.T) {
	tests := []struct {
		name       string
		item       core.Item
		cfg        Config
		wantOK     bool
		wantReason string
	}{
		{"notification with rule on", notifItem("1", "Mentioned you", "o/r", "mention"), allRules(), true, "mention"},
		{"notification with rule off", notifItem("1", "Mentioned you", "o/r", "mention"),
			Config{Rules: Rules{Notifications: false, ReviewRequests: true, AssignedIssues: true}}, false, ""},
		{"review requested", ghItem("10", "Add feature", "o/r", "alice", "review_requested"), allRules(), true, "review_requested"},
		{"assigned issue", ghItem("11", "Bug", "o/r", "alice", "assigned"), allRules(), true, "assigned"},
		{"authored only is not alertable", ghItem("12", "Mine", "o/r", "me", "authored"), allRules(), false, ""},
		{"excluded author", ghItem("13", "Bump dep", "o/r", "dependabot[bot]", "review_requested"),
			Config{Rules: allRules().Rules, ExcludeAuthors: []string{"dependabot[bot]"}}, false, ""},
		{"excluded repo", ghItem("14", "Add feature", "o/noisy", "alice", "review_requested"),
			Config{Rules: allRules().Rules, ExcludeRepos: []string{"o/noisy"}}, false, ""},
		{"bot excluded when exclude_bots is on", botItem("15", "heraldbot[bot]"),
			Config{Rules: allRules().Rules, ExcludeBots: true}, false, ""},
		{"bot kept when exclude_bots is off", botItem("16", "heraldbot[bot]"), allRules(), true, "review_requested"},
		{"person kept when exclude_bots is on", ghItem("17", "Add feature", "o/r", "alice", "review_requested"),
			Config{Rules: allRules().Rules, ExcludeBots: true}, true, "review_requested"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := alertable(tt.item, tt.cfg)
			if ok != tt.wantOK {
				t.Fatalf("alertable ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got.Reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", got.Reason, tt.wantReason)
			}
		})
	}
}

// ── Process ─────────────────────────────────────────────────────────────────

func TestProcess_ColdStartSeedsBaselineWithoutDispatch(t *testing.T) {
	store := &fakeStore{existed: false}
	ch := &fakeChannel{}
	n := New(allRules(), []Channel{ch}, store, discardLog())

	items := []core.Item{notifItem("1", "Mentioned you", "o/r", "mention")}
	if err := n.Process(context.Background(), items); err != nil {
		t.Fatal(err)
	}

	if len(ch.sent) != 0 {
		t.Errorf("cold start dispatched %d alerts, want 0", len(ch.sent))
	}
	if _, ok := store.saved["notification:1"]; !ok {
		t.Errorf("cold start should seed baseline with present items, saved=%v", store.saved)
	}
}

func TestProcess_DispatchesNewItems(t *testing.T) {
	store := &fakeStore{existed: true, seen: map[string]SeenEntry{}}
	ch := &fakeChannel{}
	n := New(allRules(), []Channel{ch}, store, discardLog())

	items := []core.Item{notifItem("1", "Mentioned you", "o/r", "mention")}
	if err := n.Process(context.Background(), items); err != nil {
		t.Fatal(err)
	}

	if len(ch.sent) != 1 {
		t.Fatalf("dispatched %d alerts, want 1", len(ch.sent))
	}
	if ch.sent[0].ID != "notification:1" {
		t.Errorf("dispatched %q", ch.sent[0].ID)
	}
	if _, ok := store.saved["notification:1"]; !ok {
		t.Errorf("new item should be persisted as seen")
	}
}

func TestProcess_SkipsAlreadySeenUnchanged(t *testing.T) {
	store := &fakeStore{existed: true, seen: map[string]SeenEntry{"notification:1": {LastSeenAt: time.Now()}}}
	ch := &fakeChannel{}
	n := New(allRules(), []Channel{ch}, store, discardLog())

	items := []core.Item{notifItem("1", "Mentioned you", "o/r", "mention")}
	if err := n.Process(context.Background(), items); err != nil {
		t.Fatal(err)
	}

	if len(ch.sent) != 0 {
		t.Errorf("already-seen unchanged item dispatched %d alerts, want 0", len(ch.sent))
	}
}

func TestProcess_ReDispatchesOnRealUpdate(t *testing.T) {
	firstSeen := time.Now().Add(-time.Hour)
	store := &fakeStore{existed: true, seen: map[string]SeenEntry{
		"notification:1": {UpdatedAt: firstSeen, LastSeenAt: firstSeen},
	}}
	ch := &fakeChannel{}
	n := New(allRules(), []Channel{ch}, store, discardLog())

	// The thread got a new comment: same ID, newer updated_at.
	items := []core.Item{notifItemAt("1", "Mentioned you", "o/r", "mention", time.Now())}
	if err := n.Process(context.Background(), items); err != nil {
		t.Fatal(err)
	}

	if len(ch.sent) != 1 || ch.sent[0].ID != "notification:1" {
		t.Fatalf("expected a re-dispatch for the updated thread, got %+v", ch.sent)
	}
}

func TestProcess_ReappearingUnchangedItemDoesNotReDispatch(t *testing.T) {
	// Item was alerted, then briefly dropped out of the snapshot (e.g.
	// paginated out or its read flag flapped), but its own updated_at never
	// changed — this must not be treated as new on either poll.
	store := &fakeStore{existed: true, seen: map[string]SeenEntry{}}
	ch := &fakeChannel{}
	n := New(allRules(), []Channel{ch}, store, discardLog())

	updatedAt := time.Now()
	item := notifItemAt("1", "Mentioned you", "o/r", "mention", updatedAt)

	if err := n.Process(context.Background(), []core.Item{item}); err != nil {
		t.Fatal(err)
	}
	if len(ch.sent) != 1 {
		t.Fatalf("first poll: dispatched %d alerts, want 1", len(ch.sent))
	}

	// Next poll: item absent (e.g. temporarily paginated out).
	store.seen = store.saved
	if err := n.Process(context.Background(), []core.Item{}); err != nil {
		t.Fatal(err)
	}

	// Reappears unchanged.
	store.seen = store.saved
	if err := n.Process(context.Background(), []core.Item{item}); err != nil {
		t.Fatal(err)
	}
	if len(ch.sent) != 1 {
		t.Fatalf("reappearance dispatched %d alerts total, want still 1", len(ch.sent))
	}
}

func TestProcess_ExpiresAbsentEntriesAfterTTL(t *testing.T) {
	start := time.Now()
	store := &fakeStore{existed: true, seen: map[string]SeenEntry{
		"notification:1": {LastSeenAt: start},
	}}
	ch := &fakeChannel{}
	cfg := allRules()
	cfg.CacheTTL = time.Hour
	n := New(cfg, []Channel{ch}, store, discardLog())
	n.now = func() time.Time { return start.Add(2 * time.Hour) } // past the TTL

	if err := n.Process(context.Background(), []core.Item{}); err != nil {
		t.Fatal(err)
	}

	if _, ok := store.saved["notification:1"]; ok {
		t.Errorf("entry absent past TTL should be expired, saved=%v", store.saved)
	}
}

func TestProcess_AppliesExclusions(t *testing.T) {
	store := &fakeStore{existed: true, seen: map[string]SeenEntry{}}
	ch := &fakeChannel{}
	cfg := allRules()
	cfg.ExcludeAuthors = []string{"dependabot[bot]"}
	n := New(cfg, []Channel{ch}, store, discardLog())

	items := []core.Item{
		ghItem("10", "Bump dep", "o/r", "dependabot[bot]", "review_requested"),
		ghItem("11", "Real PR", "o/r", "alice", "review_requested"),
	}
	if err := n.Process(context.Background(), items); err != nil {
		t.Fatal(err)
	}

	if len(ch.sent) != 1 || ch.sent[0].ID != "github:11" {
		t.Fatalf("expected only github:11 dispatched, got %+v", ch.sent)
	}
	if _, ok := store.saved["github:10"]; ok {
		t.Errorf("excluded item should not be recorded as seen")
	}
}
