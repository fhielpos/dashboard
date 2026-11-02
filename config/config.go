package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration structure.
type Config struct {
	Server    ServerConfig            `yaml:"server"`
	Scheduler SchedulerConfig         `yaml:"scheduler"`
	Plugins   map[string]PluginConfig `yaml:"plugins"`
	Dashboard DashboardConfig         `yaml:"dashboard"`
	Alerts    AlertsConfig            `yaml:"alerts"`
}

// AlertsConfig configures push alerts for newly-appeared GitHub events.
type AlertsConfig struct {
	Enabled   bool   `yaml:"enabled"`
	StatePath string `yaml:"state_path"`
	// CacheTTL is how long a dispatched item is remembered after it drops out
	// of a poll snapshot (e.g. "72h"). Defaults to alerts.DefaultCacheTTL
	// when empty. Accepts any Go duration string.
	CacheTTL       string        `yaml:"cache_ttl"`
	Rules          AlertRules    `yaml:"rules"`
	ExcludeAuthors []string      `yaml:"exclude_authors"`
	ExcludeRepos   []string      `yaml:"exclude_repos"`
	Channels       AlertChannels `yaml:"channels"`
}

// AlertRules toggles which kinds of items generate alerts.
type AlertRules struct {
	Notifications  bool `yaml:"notifications"`
	ReviewRequests bool `yaml:"review_requests"`
	AssignedIssues bool `yaml:"assigned_issues"`
}

// AlertChannels holds per-channel delivery settings.
type AlertChannels struct {
	Browser BrowserChannelConfig `yaml:"browser"`
	Ntfy    NtfyChannelConfig    `yaml:"ntfy"`
}

type BrowserChannelConfig struct {
	Enabled bool `yaml:"enabled"`
}

type NtfyChannelConfig struct {
	Enabled bool   `yaml:"enabled"`
	Server  string `yaml:"server"`
	Topic   string `yaml:"topic"`
}

type DashboardConfig struct {
	Name     string `yaml:"name"`
	Timezone string `yaml:"timezone"`
}

type ServerConfig struct {
	Port int `yaml:"port"`
}

type SchedulerConfig struct {
	// RefreshInterval is how often plugins are automatically refreshed (e.g. "5m", "1h").
	RefreshInterval string `yaml:"refresh_interval"`
}

// PluginConfig holds per-plugin settings.
type PluginConfig struct {
	Enabled bool                   `yaml:"enabled"`
	Options map[string]interface{} `yaml:",inline"`
}

// Load reads a YAML config file, expanding environment variables in string values.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	expanded := os.ExpandEnv(string(data))

	cfg := &Config{
		Server:    ServerConfig{Port: 8080},
		Scheduler: SchedulerConfig{RefreshInterval: "5m"},
	}
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}
