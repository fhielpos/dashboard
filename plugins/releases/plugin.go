package releases

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/franco/dashboard/internal/core"
)

const baseURL = "https://api.github.com"

type Plugin struct {
	token     string
	repos     []string
	client    *http.Client
	lookahead time.Duration
}

func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string        { return "releases" }
func (p *Plugin) Description() string { return "Latest releases for configured repositories" }

func (p *Plugin) Initialize(_ context.Context, config map[string]interface{}) error {
	token, ok := config["token"].(string)
	if !ok || token == "" {
		return fmt.Errorf("releases: token is required")
	}
	p.token = token

	raw, ok := config["repos"]
	if !ok {
		return fmt.Errorf("releases: repos is required")
	}
	switch v := raw.(type) {
	case []interface{}:
		for _, r := range v {
			s, ok := r.(string)
			if !ok {
				return fmt.Errorf("releases: repos must be strings")
			}
			p.repos = append(p.repos, s)
		}
	case string:
		p.repos = []string{v}
	default:
		return fmt.Errorf("releases: repos must be a list of strings")
	}

	p.lookahead = 30 * 24 * time.Hour
	if days, ok := config["lookahead_days"].(int); ok && days > 0 {
		p.lookahead = time.Duration(days) * 24 * time.Hour
	}

	p.client = &http.Client{Timeout: 15 * time.Second}
	return nil
}

func (p *Plugin) Fetch(ctx context.Context) ([]core.Item, error) {
	type result struct {
		item core.Item
		ok   bool
		err  error
	}

	results := make([]result, len(p.repos))
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup

	for i, repo := range p.repos {
		wg.Add(1)
		go func(i int, repo string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			item, ok, err := p.latestRelease(ctx, repo)
			results[i] = result{item, ok, err}
		}(i, repo)
	}
	wg.Wait()

	cutoff := time.Now().Add(-p.lookahead)
	var items []core.Item
	for _, r := range results {
		if r.err != nil || !r.ok {
			continue
		}
		pub, _ := r.item.Metadata["published_at"].(time.Time)
		if pub.Before(cutoff) {
			continue
		}
		items = append(items, r.item)
	}

	sort.Slice(items, func(i, j int) bool {
		ti, _ := items[i].Metadata["published_at"].(time.Time)
		tj, _ := items[j].Metadata["published_at"].(time.Time)
		return ti.After(tj)
	})

	return items, nil
}

func (p *Plugin) Shutdown(_ context.Context) error { return nil }

type ghRelease struct {
	ID          int64     `json:"id"`
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	Author      struct {
		Login string `json:"login"`
	} `json:"author"`
}

func (p *Plugin) latestRelease(ctx context.Context, repo string) (core.Item, bool, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", baseURL, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return core.Item{}, false, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := p.client.Do(req)
	if err != nil {
		return core.Item{}, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return core.Item{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return core.Item{}, false, fmt.Errorf("HTTP %d for %s", resp.StatusCode, repo)
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return core.Item{}, false, err
	}
	if rel.Draft {
		return core.Item{}, false, nil
	}

	title := rel.Name
	if title == "" {
		title = rel.TagName
	}

	return core.Item{
		ID:        fmt.Sprintf("release:%s:%d", repo, rel.ID),
		Plugin:    p.Name(),
		Type:      core.ItemTypeRelease,
		Title:     title,
		URL:       rel.HTMLURL,
		FetchedAt: time.Now(),
		Metadata: map[string]interface{}{
			"repository":   repo,
			"tag_name":     rel.TagName,
			"published_at": rel.PublishedAt,
			"prerelease":   rel.Prerelease,
			"author":       rel.Author.Login,
		},
	}, true, nil
}
