package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/franco/dashboard/internal/core"
)

const baseURL = "https://api.github.com"

// GitHub search returns at most 1000 results (10 pages of 100).
const maxSearchPages = 10

// Plugin fetches GitHub PRs, issues, and notifications for a user.
type Plugin struct {
	token    string
	username string
	client   *http.Client
	apiURL   string

	// botAuthors are logins treated as bots even though GitHub reports them
	// as regular users (machine accounts such as taylorbot).
	botAuthors []string
	// teamMentions are org/team slugs whose mentions should surface here.
	teamMentions []string
	// mentionWindowDays limits mention searches to recently updated items.
	// 0 means no limit.
	mentionWindowDays int
}

func New() *Plugin { return &Plugin{apiURL: baseURL} }

func (p *Plugin) Name() string        { return "github" }
func (p *Plugin) Description() string { return "GitHub pull requests and issues" }

func (p *Plugin) Initialize(ctx context.Context, config map[string]interface{}) error {
	token, _ := config["token"].(string)
	if token == "" {
		return fmt.Errorf("github: token is required")
	}
	p.token = token
	p.client = &http.Client{Timeout: 15 * time.Second}
	p.botAuthors = toStrings(config["bot_authors"])
	p.teamMentions = toStrings(config["team_mentions"])
	p.mentionWindowDays = toInt(config["mention_window_days"])

	username, err := p.resolveUsername(ctx)
	if err != nil {
		return fmt.Errorf("github: resolving authenticated user: %w", err)
	}
	p.username = username
	return nil
}

func (p *Plugin) resolveUsername(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiURL+"/user", nil)
	if err != nil {
		return "", err
	}
	p.setHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET /user returned %d", resp.StatusCode)
	}

	var u struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return "", err
	}
	if u.Login == "" {
		return "", fmt.Errorf("empty login in response")
	}
	return u.Login, nil
}

type searchDef struct {
	query    string
	category string
}

// searches builds the search queries for one fetch. now is passed in so the
// mention window moves with each refresh.
func (p *Plugin) searches(now time.Time) []searchDef {
	u := p.username
	window := ""
	if p.mentionWindowDays > 0 {
		window = " updated:>=" + now.AddDate(0, 0, -p.mentionWindowDays).Format("2006-01-02")
	}

	defs := []searchDef{
		// PRs
		{fmt.Sprintf("is:open is:pr author:%s", u), "authored"},
		{fmt.Sprintf("is:open is:pr review-requested:%s", u), "review_requested"},
		{fmt.Sprintf("is:open is:pr user-review-requested:%s", u), "review_direct"},
		{fmt.Sprintf("is:open is:pr mentions:%s -author:%s", u, u) + window, "mentioned"},
		{fmt.Sprintf("is:open is:pr author:%s review:approved", u), "approved_pending_merge"},
		// Issues
		{fmt.Sprintf("is:open is:issue assignee:%s", u), "assigned"},
		{fmt.Sprintf("is:open is:issue author:%s", u), "authored"},
		{fmt.Sprintf("is:open is:issue mentions:%s -assignee:%s -author:%s", u, u, u) + window, "mentioned"},
	}
	// Team mentions cover PRs and issues alike; the item type comes from the result.
	for _, t := range p.teamMentions {
		defs = append(defs, searchDef{fmt.Sprintf("is:open team:%s -author:%s", t, u) + window, "team_mentioned"})
	}
	for i := range defs {
		defs[i].query += " archived:false"
	}
	return defs
}

func (p *Plugin) Fetch(ctx context.Context) ([]core.Item, error) {
	type entry struct {
		item       core.Item
		nodeID     string
		categories []string
	}
	byID := make(map[string]*entry)
	var order []string

	for _, s := range p.searches(time.Now()) {
		results, err := p.searchIssues(ctx, s.query)
		if err != nil {
			// Log and continue — a partial result is better than nothing.
			slog.Warn("github: search failed", "query", s.query, "error", err)
			continue
		}
		for _, r := range results {
			if e, ok := byID[r.item.ID]; ok {
				e.categories = append(e.categories, s.category)
			} else {
				byID[r.item.ID] = &entry{item: r.item, nodeID: r.nodeID, categories: []string{s.category}}
				order = append(order, r.item.ID)
			}
		}
	}

	// Enrich PRs with CI, merge and review state.
	var prNodeIDs []string
	byNode := make(map[string]*entry)
	for _, id := range order {
		e := byID[id]
		if e.item.Type == core.ItemTypePR && e.nodeID != "" {
			prNodeIDs = append(prNodeIDs, e.nodeID)
			byNode[e.nodeID] = e
		}
	}
	states, err := p.fetchPRStates(ctx, prNodeIDs)
	if err != nil {
		slog.Warn("github: fetching PR states failed", "error", err)
	}
	for nodeID, st := range states {
		if e, ok := byNode[nodeID]; ok {
			e.item.Metadata["ci"] = st.ci
			e.item.Metadata["mergeable"] = st.mergeable
			e.item.Metadata["review_decision"] = st.reviewDecision
			e.item.Metadata["draft"] = st.draft
		}
	}

	result := make([]core.Item, 0, len(byID))
	for _, id := range order {
		e := byID[id]
		e.item.Metadata["categories"] = e.categories
		result = append(result, e.item)
	}
	return result, nil
}

func (p *Plugin) Shutdown(_ context.Context) error { return nil }

// isBot reports whether an author is automation: a GitHub App, a [bot]
// login, or a machine account listed in bot_authors.
func (p *Plugin) isBot(login, userType string) bool {
	if userType == "Bot" || strings.HasSuffix(login, "[bot]") {
		return true
	}
	for _, b := range p.botAuthors {
		if strings.EqualFold(b, login) {
			return true
		}
	}
	return false
}

// ── GitHub API types ──────────────────────────────────────────────────────────

type ghSearchResult struct {
	TotalCount int       `json:"total_count"`
	Items      []ghIssue `json:"items"`
}

type ghIssue struct {
	ID            int64     `json:"id"`
	NodeID        string    `json:"node_id"`
	Number        int       `json:"number"`
	Title         string    `json:"title"`
	HTMLURL       string    `json:"html_url"`
	RepositoryURL string    `json:"repository_url"`
	State         string    `json:"state"`
	Labels        []ghLabel `json:"labels"`
	UpdatedAt     time.Time `json:"updated_at"`
	CreatedAt     time.Time `json:"created_at"`
	PullRequest   *struct{} `json:"pull_request"`
	User          struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
}

type ghLabel struct {
	Name string `json:"name"`
}

type searchHit struct {
	item   core.Item
	nodeID string
}

type prState struct {
	ci             string // success, failure, error, pending, expected, or none
	mergeable      string // MERGEABLE, CONFLICTING, UNKNOWN
	reviewDecision string // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, or empty
	draft          bool
}

// ── Internal helpers ──────────────────────────────────────────────────────────

func (p *Plugin) searchIssues(ctx context.Context, query string) ([]searchHit, error) {
	var hits []searchHit
	for page := 1; page <= maxSearchPages; page++ {
		result, err := p.searchPage(ctx, query, page)
		if err != nil {
			return hits, err
		}
		for _, gh := range result.Items {
			hits = append(hits, searchHit{item: p.toItem(gh), nodeID: gh.NodeID})
		}
		if len(result.Items) < 100 || len(hits) >= result.TotalCount {
			break
		}
	}
	return hits, nil
}

func (p *Plugin) searchPage(ctx context.Context, query string, page int) (*ghSearchResult, error) {
	u := fmt.Sprintf("%s/search/issues?q=%s&per_page=100&sort=updated&page=%d", p.apiURL, url.QueryEscape(query), page)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	p.setHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github search returned %d", resp.StatusCode)
	}

	var result ghSearchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (p *Plugin) toItem(gh ghIssue) core.Item {
	itemType := core.ItemTypeIssue
	if gh.PullRequest != nil {
		itemType = core.ItemTypePR
	}

	tags := make([]string, 0, len(gh.Labels))
	for _, l := range gh.Labels {
		tags = append(tags, l.Name)
	}

	// repository_url is like https://api.github.com/repos/org/repo
	repoName := ""
	if gh.RepositoryURL != "" {
		parts := strings.SplitN(gh.RepositoryURL, "/repos/", 2)
		if len(parts) == 2 {
			repoName = parts[1]
		}
	}

	return core.Item{
		ID:        fmt.Sprintf("github:%d", gh.ID),
		Plugin:    p.Name(),
		Type:      itemType,
		Title:     gh.Title,
		URL:       gh.HTMLURL,
		Status:    gh.State,
		Tags:      tags,
		FetchedAt: time.Now(),
		Metadata: map[string]interface{}{
			"number":     gh.Number,
			"repository": repoName,
			"updated_at": gh.UpdatedAt,
			"created_at": gh.CreatedAt,
			"author":     gh.User.Login,
			"is_bot":     p.isBot(gh.User.Login, gh.User.Type),
			"categories": []string{}, // filled by Fetch
		},
	}
}

const prStateQuery = `query($ids: [ID!]!) {
  nodes(ids: $ids) {
    ... on PullRequest {
      id
      isDraft
      mergeable
      reviewDecision
      commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
    }
  }
}`

// prStateBatch is kept small: GitHub answers 502 when one query resolves the
// check rollups of ~100 PRs.
const prStateBatch = 25

// fetchPRStates looks up CI, merge and review state for PRs by node ID. The
// search API returns none of it. A failed batch is skipped so the others
// still get their state; the first error is returned.
func (p *Plugin) fetchPRStates(ctx context.Context, nodeIDs []string) (map[string]prState, error) {
	out := make(map[string]prState, len(nodeIDs))
	var firstErr error
	for start := 0; start < len(nodeIDs); start += prStateBatch {
		end := min(start+prStateBatch, len(nodeIDs))
		if err := p.fetchPRStateBatch(ctx, nodeIDs[start:end], out); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return out, firstErr
}

func (p *Plugin) fetchPRStateBatch(ctx context.Context, ids []string, out map[string]prState) error {
	body, err := json.Marshal(map[string]interface{}{
		"query":     prStateQuery,
		"variables": map[string]interface{}{"ids": ids},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiURL+"/graphql", bytes.NewReader(body))
	if err != nil {
		return err
	}
	p.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github graphql returned %d", resp.StatusCode)
	}

	var result struct {
		Data struct {
			Nodes []*struct {
				ID             string `json:"id"`
				IsDraft        bool   `json:"isDraft"`
				Mergeable      string `json:"mergeable"`
				ReviewDecision string `json:"reviewDecision"`
				Commits        struct {
					Nodes []struct {
						Commit struct {
							StatusCheckRollup *struct {
								State string `json:"state"`
							} `json:"statusCheckRollup"`
						} `json:"commit"`
					} `json:"nodes"`
				} `json:"commits"`
			} `json:"nodes"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	for _, n := range result.Data.Nodes {
		if n == nil || n.ID == "" {
			continue
		}
		ci := "none"
		if len(n.Commits.Nodes) > 0 && n.Commits.Nodes[0].Commit.StatusCheckRollup != nil {
			ci = strings.ToLower(n.Commits.Nodes[0].Commit.StatusCheckRollup.State)
		}
		out[n.ID] = prState{ci: ci, mergeable: n.Mergeable, reviewDecision: n.ReviewDecision, draft: n.IsDraft}
	}
	return nil
}

func (p *Plugin) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

// toStrings converts a YAML list (decoded as []interface{}) to []string.
func toStrings(v interface{}) []string {
	list, _ := v.([]interface{})
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s, ok := x.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func toInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}
