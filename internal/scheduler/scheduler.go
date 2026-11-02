package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/franco/dashboard/internal/core"
)

// Notifier is dispatched the fresh item snapshot after every refresh.
type Notifier interface {
	Process(ctx context.Context, items []core.Item) error
}

// Scheduler periodically refreshes all registered plugins.
type Scheduler struct {
	registry *core.Registry
	interval time.Duration
	notifier Notifier
	stop     chan struct{}
}

// New builds a Scheduler. notifier may be nil to disable alerting.
func New(registry *core.Registry, interval time.Duration, notifier Notifier) *Scheduler {
	return &Scheduler{
		registry: registry,
		interval: interval,
		notifier: notifier,
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

	if s.notifier != nil {
		if err := s.notifier.Process(ctx, s.registry.Items("", "")); err != nil {
			slog.Error("scheduler: alert processing failed", "error", err)
		}
	}
}

// Stop shuts down the scheduler.
func (s *Scheduler) Stop() {
	close(s.stop)
}
