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

	// Start background refresh scheduler.
	refreshInterval, err := time.ParseDuration(cfg.Scheduler.RefreshInterval)
	if err != nil {
		return fmt.Errorf("invalid scheduler.refresh_interval: %w", err)
	}
	sched := scheduler.New(registry, refreshInterval)
	sched.Start(ctx)

	// Start HTTP server.
	name := cfg.Dashboard.Name
	if name == "" {
		name = "there"
	}
	srv, err := api.NewServer(cfg.Server.Port, registry, name, cfg.Dashboard.Timezone, Commit)
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
