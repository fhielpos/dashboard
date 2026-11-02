package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/franco/dashboard/internal/core"
)

func TestIsBot(t *testing.T) {
	p := &Plugin{botAuthors: toStrings([]interface{}{"taylorbot"})}
	cases := []struct {
		login, typ string
		want       bool
	}{
		{"renovate[bot]", "Bot", true},
		{"heraldbot[bot]", "", true}, // suffix alone is enough
		{"taylorbot", "User", true},  // machine account from bot_authors
		{"TaylorBot", "User", true},  // case-insensitive
		{"stone-z", "User", false},
		{"robot-fan", "User", false}, // "bot" in the name is not enough
	}
	for _, c := range cases {
		if got := p.isBot(c.login, c.typ); got != c.want {
			t.Errorf("isBot(%q, %q) = %v, want %v", c.login, c.typ, got, c.want)
		}
	}
}

func TestSearches(t *testing.T) {
	p := &Plugin{username: "me", mentionWindowDays: 30, teamMentions: []string{"org/team-a"}}
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	byCat := map[string][]string{}
	for _, s := range p.searches(now) {
		if !strings.HasSuffix(s.query, " archived:false") {
			t.Errorf("query %q does not exclude archived repos", s.query)
		}
		byCat[s.category] = append(byCat[s.category], s.query)
	}

	for _, q := range byCat["mentioned"] {
		if !strings.Contains(q, "updated:>=2026-10-03") {
			t.Errorf("mention query %q missing 30-day window", q)
		}
	}
	for _, q := range byCat["assigned"] {
		if strings.Contains(q, "updated:") {
			t.Errorf("assigned query %q must not be windowed", q)
		}
	}
	if got := byCat["review_direct"]; len(got) != 1 || !strings.Contains(got[0], "user-review-requested:me") {
		t.Errorf("review_direct queries = %v", got)
	}
	if got := byCat["team_mentioned"]; len(got) != 1 || !strings.Contains(got[0], "team:org/team-a") {
		t.Errorf("team_mentioned queries = %v", got)
	}

	p.mentionWindowDays = 0
	for _, s := range p.searches(now) {
		if strings.Contains(s.query, "updated:") {
			t.Errorf("window disabled but query %q is windowed", s.query)
		}
	}
}

func TestFetchEnrichesPRs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/issues":
			q := r.URL.Query().Get("q")
			var items []map[string]interface{}
			if strings.Contains(q, "review-requested:me") && !strings.Contains(q, "user-review-requested") {
				items = append(items, map[string]interface{}{
					"id": 1, "node_id": "PR_1", "number": 10, "title": "bump",
					"repository_url": "https://api.github.com/repos/org/repo",
					"pull_request":   map[string]interface{}{},
					"user":           map[string]interface{}{"login": "renovate[bot]", "type": "Bot"},
				})
			}
			if strings.Contains(q, "assignee:me") && !strings.Contains(q, "-assignee") {
				items = append(items, map[string]interface{}{
					"id": 2, "node_id": "I_2", "number": 20, "title": "task",
					"user": map[string]interface{}{"login": "stone-z", "type": "User"},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"total_count": len(items), "items": items})
		case "/graphql":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"nodes": []interface{}{
				map[string]interface{}{
					"id": "PR_1", "isDraft": false, "mergeable": "MERGEABLE", "reviewDecision": "REVIEW_REQUIRED",
					"commits": map[string]interface{}{"nodes": []interface{}{
						map[string]interface{}{"commit": map[string]interface{}{"statusCheckRollup": map[string]interface{}{"state": "SUCCESS"}}},
					}},
				},
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := &Plugin{username: "me", token: "t", client: srv.Client(), apiURL: srv.URL}
	items, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	byID := map[string]core.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}

	pr := byID["github:1"]
	if pr.Type != core.ItemTypePR {
		t.Errorf("PR type = %q", pr.Type)
	}
	if pr.Metadata["is_bot"] != true {
		t.Errorf("renovate[bot] not marked as bot")
	}
	if pr.Metadata["ci"] != "success" || pr.Metadata["mergeable"] != "MERGEABLE" {
		t.Errorf("PR state not enriched: ci=%v mergeable=%v", pr.Metadata["ci"], pr.Metadata["mergeable"])
	}
	if cats := pr.Metadata["categories"].([]string); len(cats) != 1 || cats[0] != "review_requested" {
		t.Errorf("PR categories = %v", cats)
	}

	issue := byID["github:2"]
	if issue.Type != core.ItemTypeIssue || issue.Metadata["is_bot"] != false {
		t.Errorf("issue = %+v", issue)
	}
	if _, ok := issue.Metadata["ci"]; ok {
		t.Errorf("issue should not carry CI state")
	}
}
