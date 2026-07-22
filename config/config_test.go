package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_AlertsSection(t *testing.T) {
	yaml := `
alerts:
  enabled: true
  state_path: "/data/alerts.json"
  rules:
    notifications: true
    review_requests: true
    assigned_issues: false
  exclude_authors: ["dependabot[bot]"]
  channels:
    browser:
      enabled: true
    ntfy:
      enabled: true
      server: "https://ntfy.sh"
      topic: "${TEST_NTFY_TOPIC}"
`
	t.Setenv("TEST_NTFY_TOPIC", "my-secret-topic")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	a := cfg.Alerts
	if !a.Enabled || a.StatePath != "/data/alerts.json" {
		t.Errorf("basic fields wrong: %+v", a)
	}
	if !a.Rules.Notifications || !a.Rules.ReviewRequests || a.Rules.AssignedIssues {
		t.Errorf("rules wrong: %+v", a.Rules)
	}
	if len(a.ExcludeAuthors) != 1 || a.ExcludeAuthors[0] != "dependabot[bot]" {
		t.Errorf("exclude_authors wrong: %+v", a.ExcludeAuthors)
	}
	if !a.Channels.Browser.Enabled {
		t.Errorf("browser channel should be enabled")
	}
	if !a.Channels.Ntfy.Enabled || a.Channels.Ntfy.Server != "https://ntfy.sh" {
		t.Errorf("ntfy channel wrong: %+v", a.Channels.Ntfy)
	}
	if a.Channels.Ntfy.Topic != "my-secret-topic" {
		t.Errorf("ntfy topic env expansion failed: %q", a.Channels.Ntfy.Topic)
	}
}
