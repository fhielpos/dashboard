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
