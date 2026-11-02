package api

import (
	"testing"
	"time"

	"github.com/franco/dashboard/internal/core"
)

func pr(isBot bool, ci, mergeable string, draft bool) core.Item {
	return core.Item{Type: core.ItemTypePR, Metadata: map[string]interface{}{
		"is_bot": isBot, "ci": ci, "mergeable": mergeable, "draft": draft,
	}}
}

func TestMatchesWho(t *testing.T) {
	human := pr(false, "failure", "MERGEABLE", false)
	greenBot := pr(true, "success", "MERGEABLE", false)
	conflictBot := pr(true, "success", "CONFLICTING", false)
	greenDraft := pr(false, "success", "MERGEABLE", true)

	cases := []struct {
		item core.Item
		who  string
		want bool
	}{
		{human, "people", true},
		{human, "bots", false},
		{greenBot, "bots", true},
		{greenBot, "ready", true},
		{conflictBot, "ready", false},
		{greenDraft, "ready", false},
		{human, "ready", false},
		{human, "all", true},
	}
	for i, c := range cases {
		if got := matchesWho(c.item, c.who); got != c.want {
			t.Errorf("case %d: matchesWho(%v, %q) = %v, want %v", i, c.item.Metadata, c.who, got, c.want)
		}
	}
}

func TestStateBadges(t *testing.T) {
	labels := func(item core.Item) map[string]bool {
		m := map[string]bool{}
		for _, b := range stateBadges(item) {
			m[b.Label] = true
		}
		return m
	}

	got := labels(pr(true, "failure", "CONFLICTING", false))
	for _, want := range []string{"bot", "CI failing", "conflicts"} {
		if !got[want] {
			t.Errorf("bot PR missing %q badge: %v", want, got)
		}
	}

	stale := core.Item{Type: core.ItemTypeIssue, Metadata: map[string]interface{}{
		"updated_at": time.Now().Add(-40 * 24 * time.Hour),
	}}
	if !labels(stale)["stale"] {
		t.Errorf("40-day-old issue not marked stale")
	}
	fresh := core.Item{Type: core.ItemTypeIssue, Metadata: map[string]interface{}{
		"updated_at": time.Now().Add(-2 * 24 * time.Hour),
	}}
	if labels(fresh)["stale"] {
		t.Errorf("2-day-old issue marked stale")
	}
}
