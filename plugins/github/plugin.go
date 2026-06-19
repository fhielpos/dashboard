package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/franco/dashboard/internal/core"
)

const baseURL = "https://api.github.com"

// Plugin fetches GitHub PRs, issues, and notifications for a user.
type Plugin struct {
	token    string
	username string
	client   *http.Client
}

func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string        { return "github" }
func (p *Plugin) Description() string { return "GitHub pull requests and issues" }

func (p *Plugin) Initialize(ctx context.Context, config map[string]interface{}) error {
	token, _ := config["token"].(string)
	if token == "" {
		return fmt.Errorf("github: token is required")
	}
	p.token = token
	p.client = &http.Client{Timeout: 15 * time.Second}

	username, err := p.resolveUsername(ctx)
	if err != nil {
		return fmt.Errorf("github: resolving authenticated user: %w", err)
	}
	p.username = username
	return nil
}

func (p *Plugin) resolveUsername(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/user", nil)
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

func (p *Plugin) Fetch(ctx context.Context) ([]core.Item, error) {
	type searchDef struct {
		query    string
		category string
		typ      core.ItemType
	}

	u := p.username
	searches := []searchDef{
		// PRs
		{fmt.Sprintf("is:open is:pr author:%s", u), "authored", core.ItemTypePR},
		{fmt.Sprintf("is:open is:pr review-requested:%s", u), "review_requested", core.ItemTypePR},
		{fmt.Sprintf("is:open is:pr mentions:%s -author:%s", u, u), "mentioned", core.ItemTypePR},
		{fmt.Sprintf("is:open is:pr author:%s review:approved", u), "approved_pending_merge", core.ItemTypePR},
		// Issues
		{fmt.Sprintf("is:open is:issue assignee:%s", u), "assigned", core.ItemTypeIssue},
		{fmt.Sprintf("is:open is:issue author:%s", u), "authored", core.ItemTypeIssue},
		{fmt.Sprintf("is:open is:issue mentions:%s -assignee:%s -author:%s", u, u, u), "mentioned", core.ItemTypeIssue},
	}

	type entry struct {
		item       core.Item
		categories []string
	}
	byID := make(map[string]*entry)

	for _, s := range searches {
		items, err := p.searchIssues(ctx, s.query, s.typ)
		if err != nil {
			// Log and continue — a partial result is better than nothing.
			continue
		}
		for _, item := range items {
			if e, ok := byID[item.ID]; ok {
				e.categories = append(e.categories, s.category)
			} else {
				byID[item.ID] = &entry{item: item, categories: []string{s.category}}
			}
		}
	}

	result := make([]core.Item, 0, len(byID))
	for _, e := range byID {
		e.item.Metadata["categories"] = e.categories
		result = append(result, e.item)
	}
	return result, nil
}

func (p *Plugin) Shutdown(_ context.Context) error { return nil }

// ── GitHub API types ──────────────────────────────────────────────────────────

type ghSearchResult struct {
	Items []ghIssue `json:"items"`
}

type ghIssue struct {
	ID            int64     `json:"id"`
	Number        int       `json:"number"`
	Title         string    `json:"title"`
	HTMLURL       string    `json:"html_url"`
	RepositoryURL string    `json:"repository_url"`
	State         string    `json:"state"`
	Labels        []ghLabel `json:"labels"`
	UpdatedAt     time.Time `json:"updated_at"`
	CreatedAt     time.Time `json:"created_at"`
	PullRequest   *struct{} `json:"pull_request"`
}

type ghLabel struct{ Name string `json:"name"` }

// ── Internal helpers ──────────────────────────────────────────────────────────

func (p *Plugin) searchIssues(ctx context.Context, query string, typ core.ItemType) ([]core.Item, error) {
	url := fmt.Sprintf("%s/search/issues?q=%s&per_page=100&sort=updated", baseURL, encodeQuery(query))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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

	now := time.Now()
	items := make([]core.Item, 0, len(result.Items))
	for _, gh := range result.Items {
		itemType := typ
		if itemType == core.ItemTypePR && gh.PullRequest == nil {
			itemType = core.ItemTypeIssue
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

		items = append(items, core.Item{
			ID:        fmt.Sprintf("github:%d", gh.ID),
			Plugin:    p.Name(),
			Type:      itemType,
			Title:     gh.Title,
			URL:       gh.HTMLURL,
			Status:    gh.State,
			Tags:      tags,
			FetchedAt: now,
			Metadata: map[string]interface{}{
				"number":     gh.Number,
				"repository": repoName,
				"updated_at": gh.UpdatedAt,
				"created_at": gh.CreatedAt,
				"categories": []string{}, // filled by Fetch
			},
		})
	}
	return items, nil
}

func (p *Plugin) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

func encodeQuery(q string) string {
	out := make([]byte, 0, len(q))
	for i := 0; i < len(q); i++ {
		switch q[i] {
		case ' ':
			out = append(out, '+')
		default:
			out = append(out, q[i])
		}
	}
	return string(out)
}
