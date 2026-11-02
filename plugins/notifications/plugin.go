package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/franco/dashboard/internal/core"
)

const (
	baseURL          = "https://api.github.com"
	ItemTypeNotification core.ItemType = "notification"
)

// Plugin fetches GitHub notifications (mentions, review requests, etc.).
type Plugin struct {
	token  string
	client *http.Client
	// onlyUnread filters to unread notifications only.
	onlyUnread bool
}

func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string        { return "notifications" }
func (p *Plugin) Description() string { return "GitHub notifications" }

func (p *Plugin) Initialize(_ context.Context, config map[string]interface{}) error {
	token, _ := config["token"].(string)
	if token == "" {
		return fmt.Errorf("notifications: token is required")
	}
	p.token = token
	p.client = &http.Client{Timeout: 15 * time.Second}

	onlyUnread := true // default: only unread
	if v, ok := config["all"].(bool); ok {
		onlyUnread = !v
	}
	p.onlyUnread = onlyUnread
	return nil
}

func (p *Plugin) Fetch(ctx context.Context) ([]core.Item, error) {
	url := fmt.Sprintf("%s/notifications?per_page=50", baseURL)
	if p.onlyUnread {
		url += "&all=false"
	} else {
		url += "&all=true"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("notifications API returned %d", resp.StatusCode)
	}

	var notifications []ghNotification
	if err := json.NewDecoder(resp.Body).Decode(&notifications); err != nil {
		return nil, err
	}

	now := time.Now()
	items := make([]core.Item, 0, len(notifications))
	for _, n := range notifications {
		if skipReason(n.Reason) {
			continue
		}
		htmlURL := apiURLToHTML(n.Subject.URL, n.Repository.HTMLURL)
		items = append(items, core.Item{
			ID:        fmt.Sprintf("notification:%s", n.ID),
			Plugin:    p.Name(),
			Type:      ItemTypeNotification,
			Title:     n.Subject.Title,
			URL:       htmlURL,
			FetchedAt: now,
			Metadata: map[string]interface{}{
				"reason":      n.Reason,
				"unread":      n.Unread,
				"subject_type": n.Subject.Type,
				"repository":  n.Repository.FullName,
				"updated_at":  n.UpdatedAt,
			},
		})
	}
	return items, nil
}

// skipReason reports whether a notification reason is already covered by the
// GitHub PR plugin's own review-requested/assigned categories, so surfacing
// it here too would double-fire a notification for the same event.
func skipReason(reason string) bool {
	return reason == "review_requested" || reason == "assign"
}

func (p *Plugin) Shutdown(_ context.Context) error { return nil }

// ── WritablePlugin — mark as read ─────────────────────────────────────────────

func (p *Plugin) CreateItem(_ context.Context, _ core.Item) (core.Item, error) {
	return core.Item{}, fmt.Errorf("notifications: create not supported")
}

func (p *Plugin) UpdateItem(_ context.Context, _ string, _ map[string]interface{}) (core.Item, error) {
	return core.Item{}, fmt.Errorf("notifications: update not supported")
}

// DeleteItem marks the notification thread as read on GitHub.
// id is the item ID in the form "notification:{threadID}".
func (p *Plugin) DeleteItem(ctx context.Context, id string) error {
	threadID := strings.TrimPrefix(id, "notification:")
	url := fmt.Sprintf("%s/notifications/threads/%s", baseURL, threadID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// GitHub returns 205 Reset Content on success.
	if resp.StatusCode != http.StatusResetContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mark as read returned %d", resp.StatusCode)
	}
	return nil
}

// ── GitHub API types ──────────────────────────────────────────────────────────

type ghNotification struct {
	ID         string `json:"id"`
	Repository struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"repository"`
	Subject struct {
		Title string `json:"title"`
		URL   string `json:"url"`
		Type  string `json:"type"` // Issue, PullRequest, Commit, Release
	} `json:"subject"`
	Reason    string    `json:"reason"`
	Unread    bool      `json:"unread"`
	UpdatedAt time.Time `json:"updated_at"`
}

// apiURLToHTML converts a GitHub API URL to an HTML URL.
// e.g. https://api.github.com/repos/owner/repo/issues/1
//   → https://github.com/owner/repo/issues/1
func apiURLToHTML(apiURL, repoHTMLURL string) string {
	if apiURL == "" {
		return repoHTMLURL
	}
	// Strip /repos prefix and replace api hostname
	u := strings.Replace(apiURL, "https://api.github.com/repos/", "https://github.com/", 1)
	// GitHub uses /pull/ in HTML but /pulls/ in API
	u = strings.Replace(u, "/pulls/", "/pull/", 1)
	return u
}
