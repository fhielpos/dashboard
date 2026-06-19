package status

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/franco/dashboard/internal/core"
)

// Plugin polls Atlassian Statuspage endpoints (used by GitHub, Anthropic, etc.)
type Plugin struct {
	services []service
	client   *http.Client
}

type service struct {
	name string
	url  string // base URL, e.g. https://www.githubstatus.com
}

func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string        { return "status" }
func (p *Plugin) Description() string { return "Service status from Statuspage endpoints" }

func (p *Plugin) Initialize(_ context.Context, config map[string]interface{}) error {
	raw, ok := config["services"]
	if !ok {
		return fmt.Errorf("status: services is required")
	}
	list, ok := raw.([]interface{})
	if !ok {
		return fmt.Errorf("status: services must be a list")
	}
	for _, entry := range list {
		m, ok := entry.(map[string]interface{})
		if !ok {
			return fmt.Errorf("status: each service must be a map with name and url")
		}
		name, _ := m["name"].(string)
		url, _ := m["url"].(string)
		if name == "" || url == "" {
			return fmt.Errorf("status: each service requires name and url")
		}
		p.services = append(p.services, service{name: name, url: strings.TrimRight(url, "/")})
	}
	p.client = &http.Client{Timeout: 10 * time.Second}
	return nil
}

func (p *Plugin) Fetch(ctx context.Context) ([]core.Item, error) {
	now := time.Now()
	items := make([]core.Item, 0, len(p.services))
	for _, svc := range p.services {
		indicator, description, err := p.fetchStatus(ctx, svc.url)
		if err != nil {
			// Show as unknown rather than failing the whole fetch.
			indicator = "unknown"
			description = err.Error()
		}
		items = append(items, core.Item{
			ID:        fmt.Sprintf("status:%s", svc.name),
			Plugin:    p.Name(),
			Type:      core.ItemTypeStatus,
			Title:     svc.name,
			URL:       svc.url,
			Status:    indicator,
			FetchedAt: now,
			Metadata: map[string]interface{}{
				"indicator":   indicator,
				"description": description,
			},
		})
	}
	return items, nil
}

func (p *Plugin) Shutdown(_ context.Context) error { return nil }

type statuspageResponse struct {
	Status struct {
		Indicator   string `json:"indicator"`
		Description string `json:"description"`
	} `json:"status"`
}

func (p *Plugin) fetchStatus(ctx context.Context, baseURL string) (indicator, description string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v2/status.json", nil)
	if err != nil {
		return "", "", err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var result statuspageResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", err
	}
	return result.Status.Indicator, result.Status.Description, nil
}
