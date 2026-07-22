package alerts

import (
	"context"
	"testing"
	"time"
)

func TestBuffer_SinceReturnsOnlyNewer(t *testing.T) {
	b := NewBuffer(10, time.Hour)
	b.Add(Alert{ID: "a"})
	b.Add(Alert{ID: "b"})
	b.Add(Alert{ID: "c"})

	all, cursor := b.Since(0)
	if len(all) != 3 || cursor != 3 {
		t.Fatalf("Since(0) = %d alerts, cursor %d; want 3, 3", len(all), cursor)
	}

	rest, cursor := b.Since(2)
	if len(rest) != 1 || rest[0].ID != "c" || cursor != 3 {
		t.Fatalf("Since(2) = %+v cursor %d; want [c], 3", rest, cursor)
	}

	none, cursor := b.Since(3)
	if len(none) != 0 || cursor != 3 {
		t.Fatalf("Since(3) = %d alerts cursor %d; want 0, 3", len(none), cursor)
	}
}

func TestBuffer_TTLExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	b := NewBuffer(10, 10*time.Minute)
	b.now = func() time.Time { return now }

	b.Add(Alert{ID: "a"})
	now = now.Add(11 * time.Minute)

	got, _ := b.Since(0)
	if len(got) != 0 {
		t.Errorf("expired alert should not be returned, got %+v", got)
	}
}

func TestBuffer_CapDropsOldest(t *testing.T) {
	b := NewBuffer(2, time.Hour)
	b.Add(Alert{ID: "a"})
	b.Add(Alert{ID: "b"})
	b.Add(Alert{ID: "c"})

	got, _ := b.Since(0)
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "c" {
		t.Errorf("cap should keep newest 2, got %+v", got)
	}
}

func TestBrowserChannel_SendAppendsToBuffer(t *testing.T) {
	b := NewBuffer(10, time.Hour)
	ch := NewBrowserChannel(b)
	if err := ch.Send(context.Background(), Alert{ID: "a", Title: "hi"}); err != nil {
		t.Fatal(err)
	}
	got, _ := b.Since(0)
	if len(got) != 1 || got[0].Title != "hi" {
		t.Errorf("send should buffer the alert, got %+v", got)
	}
}
