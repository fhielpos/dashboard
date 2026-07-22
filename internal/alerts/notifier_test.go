package alerts

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/franco/dashboard/internal/core"
)

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func notifItem(id, title, repo, reason string) core.Item {
	return core.Item{
		ID:     "notification:" + id,
		Plugin: "notifications",
		Type:   "notification",
		Title:  title,
		Metadata: map[string]interface{}{
			"reason":     reason,
			"repository": repo,
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

func allRules() Config {
	return Config{Rules: Rules{Notifications: true, ReviewRequests: true, AssignedIssues: true}}
}

// ── fakes ─────────────────────────────────────────────────────────────────────

type fakeStore struct {
	seen    map[string]bool
	existed bool
	saved   map[string]bool
}

func (f *fakeStore) Load() (map[string]bool, bool, error) {
	out := make(map[string]bool, len(f.seen))
	for k := range f.seen {
		out[k] = true
	}
	return out, f.existed, nil
}

func (f *fakeStore) Save(s map[string]bool) error {
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
	if !store.saved["notification:1"] {
		t.Errorf("cold start should seed baseline with present items, saved=%v", store.saved)
	}
}

func TestProcess_DispatchesNewItems(t *testing.T) {
	store := &fakeStore{existed: true, seen: map[string]bool{}}
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
	if !store.saved["notification:1"] {
		t.Errorf("new item should be persisted as seen")
	}
}

func TestProcess_SkipsAlreadySeen(t *testing.T) {
	store := &fakeStore{existed: true, seen: map[string]bool{"notification:1": true}}
	ch := &fakeChannel{}
	n := New(allRules(), []Channel{ch}, store, discardLog())

	items := []core.Item{notifItem("1", "Mentioned you", "o/r", "mention")}
	if err := n.Process(context.Background(), items); err != nil {
		t.Fatal(err)
	}

	if len(ch.sent) != 0 {
		t.Errorf("already-seen item dispatched %d alerts, want 0", len(ch.sent))
	}
}

func TestProcess_AppliesExclusions(t *testing.T) {
	store := &fakeStore{existed: true, seen: map[string]bool{}}
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
	if store.saved["github:10"] {
		t.Errorf("excluded item should not be recorded as seen")
	}
}
