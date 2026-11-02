package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/franco/dashboard/config"
	"github.com/franco/dashboard/internal/alerts"
	"github.com/franco/dashboard/internal/api"
	"github.com/franco/dashboard/internal/core"
	"github.com/franco/dashboard/internal/scheduler"
	calendarplugin "github.com/franco/dashboard/plugins/calendar"
	githubplugin   "github.com/franco/dashboard/plugins/github"
	notifplugin    "github.com/franco/dashboard/plugins/notifications"
	releasesplugin "github.com/franco/dashboard/plugins/releases"
	statusplugin   "github.com/franco/dashboard/plugins/status"
	tasksplugin    "github.com/franco/dashboard/plugins/tasks"
)

// Commit is injected at build time via -ldflags "-X main.Commit=<hash>".
var Commit = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	registry := core.NewRegistry()

	// Register and initialize enabled plugins.
	available := map[string]core.Plugin{
		"github":        githubplugin.New(),
		"calendar":      calendarplugin.New(),
		"tasks":         tasksplugin.New(),
		"notifications": notifplugin.New(),
		"releases":      releasesplugin.New(),
		"status":        statusplugin.New(),
	}

	for name, plugin := range available {
		pcfg, ok := cfg.Plugins[name]
		if !ok || !pcfg.Enabled {
			slog.Info("plugin disabled", "name", name)
			continue
		}

		opts := pcfg.Options
		if opts == nil {
			opts = make(map[string]interface{})
		}

		if err := plugin.Initialize(ctx, opts); err != nil {
			return fmt.Errorf("initializing plugin %s: %w", name, err)
		}
		registry.Register(plugin)
		slog.Info("plugin registered", "name", name)
	}

	// Build push-alert notifier and browser buffer from config.
	notifier, alertBuf := buildAlerts(cfg.Alerts)

	// Start background refresh scheduler.
	refreshInterval, err := time.ParseDuration(cfg.Scheduler.RefreshInterval)
	if err != nil {
		return fmt.Errorf("invalid scheduler.refresh_interval: %w", err)
	}
	sched := scheduler.New(registry, refreshInterval, notifier)
	sched.Start(ctx)

	// Start HTTP server.
	name := cfg.Dashboard.Name
	if name == "" {
		name = "there"
	}
	srv, err := api.NewServer(cfg.Server.Port, registry, alertBuf, name, cfg.Dashboard.Timezone, Commit)
	if err != nil {
		return fmt.Errorf("creating server: %w", err)
	}
	slog.Info("starting server", "port", cfg.Server.Port)

	serverErr := make(chan error, 1)
	go func() {
		if err := srv.Start(); !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
		slog.Info("shutting down")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	sched.Stop()
	return srv.Shutdown(shutdownCtx)
}

// buildAlerts constructs the push-alert notifier and the browser buffer from
// config. It returns (nil, nil) when alerting is disabled or misconfigured,
// logging a warning rather than failing startup. The browser buffer is shared
// between the browser channel and the HTTP endpoint that the dashboard polls.
func buildAlerts(cfg config.AlertsConfig) (scheduler.Notifier, *alerts.Buffer) {
	if !cfg.Enabled {
		return nil, nil
	}

	var (
		channels []alerts.Channel
		alertBuf *alerts.Buffer
	)

	if cfg.Channels.Browser.Enabled {
		alertBuf = alerts.NewBuffer(50, 15*time.Minute)
		channels = append(channels, alerts.NewBrowserChannel(alertBuf))
	}

	if cfg.Channels.Ntfy.Enabled {
		server := cfg.Channels.Ntfy.Server
		if server == "" {
			server = "https://ntfy.sh"
		}
		if cfg.Channels.Ntfy.Topic == "" {
			slog.Warn("alerts: ntfy enabled but no topic configured; skipping ntfy channel")
		} else {
			channels = append(channels, alerts.NewNtfyChannel(server, cfg.Channels.Ntfy.Topic))
		}
	}

	if len(channels) == 0 {
		slog.Warn("alerts: enabled but no usable channels; alerting disabled")
		return nil, alertBuf
	}

	statePath := cfg.StatePath
	if statePath == "" {
		statePath = "/data/alerts.json"
	}

	var cacheTTL time.Duration
	if cfg.CacheTTL != "" {
		d, err := time.ParseDuration(cfg.CacheTTL)
		if err != nil {
			slog.Warn("alerts: invalid cache_ttl, using default", "value", cfg.CacheTTL, "default", alerts.DefaultCacheTTL, "error", err)
		} else {
			cacheTTL = d
		}
	}

	acfg := alerts.Config{
		Rules: alerts.Rules{
			Notifications:  cfg.Rules.Notifications,
			ReviewRequests: cfg.Rules.ReviewRequests,
			AssignedIssues: cfg.Rules.AssignedIssues,
		},
		ExcludeAuthors: cfg.ExcludeAuthors,
		ExcludeRepos:   cfg.ExcludeRepos,
		CacheTTL:       cacheTTL,
	}
	notifier := alerts.New(acfg, channels, alerts.NewFileStore(statePath), slog.Default())
	slog.Info("alerts enabled", "channels", len(channels))
	return notifier, alertBuf
}
