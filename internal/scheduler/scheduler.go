package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/franco/dashboard/internal/core"
)

// Scheduler periodically refreshes all registered plugins.
type Scheduler struct {
	registry *core.Registry
	interval time.Duration
	stop     chan struct{}
}

func New(registry *core.Registry, interval time.Duration) *Scheduler {
	return &Scheduler{
		registry: registry,
		interval: interval,
		stop:     make(chan struct{}),
	}
}

// Start runs the refresh loop in the background.
func (s *Scheduler) Start(ctx context.Context) {
	go s.run(ctx)
}

func (s *Scheduler) run(ctx context.Context) {
	// Initial fetch on startup.
	s.refresh(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.refresh(ctx)
		case <-s.stop:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scheduler) refresh(ctx context.Context) {
	slog.Info("scheduler: refreshing all plugins")
	errs := s.registry.RefreshAll(ctx)
	for name, err := range errs {
		slog.Error("scheduler: plugin refresh failed", "plugin", name, "error", err)
	}
}

// Stop shuts down the scheduler.
func (s *Scheduler) Stop() {
	close(s.stop)
}
