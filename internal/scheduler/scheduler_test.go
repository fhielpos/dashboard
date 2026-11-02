package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/franco/dashboard/internal/core"
)

type fakePlugin struct{ items []core.Item }

func (p *fakePlugin) Name() string                                        { return "fake" }
func (p *fakePlugin) Description() string                                 { return "fake" }
func (p *fakePlugin) Initialize(context.Context, map[string]interface{}) error { return nil }
func (p *fakePlugin) Fetch(context.Context) ([]core.Item, error)          { return p.items, nil }
func (p *fakePlugin) Shutdown(context.Context) error                      { return nil }

type fakeNotifier struct{ got []core.Item }

func (n *fakeNotifier) Process(_ context.Context, items []core.Item) error {
	n.got = items
	return nil
}

func TestRefresh_InvokesNotifierWithItems(t *testing.T) {
	reg := core.NewRegistry()
	reg.Register(&fakePlugin{items: []core.Item{{ID: "x"}}})

	notifier := &fakeNotifier{}
	s := New(reg, time.Minute, notifier)
	s.refresh(context.Background())

	if len(notifier.got) != 1 || notifier.got[0].ID != "x" {
		t.Fatalf("notifier should receive refreshed items, got %+v", notifier.got)
	}
}

func TestRefresh_NilNotifierIsNoop(t *testing.T) {
	reg := core.NewRegistry()
	reg.Register(&fakePlugin{items: []core.Item{{ID: "x"}}})

	s := New(reg, time.Minute, nil)
	s.refresh(context.Background()) // must not panic
}
