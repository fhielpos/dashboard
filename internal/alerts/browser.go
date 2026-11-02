package alerts

import (
	"context"
	"sync"
	"time"
)

// Buffer holds recent alerts for delivery to the browser. The dashboard polls
// it with a monotonically increasing cursor and fires a Notification per new
// alert. Entries are dropped once they exceed the TTL or the capacity.
type Buffer struct {
	mu    sync.Mutex
	seq   int64
	items []buffered
	ttl   time.Duration
	max   int
	now   func() time.Time
}

type buffered struct {
	seq   int64
	at    time.Time
	alert Alert
}

// NewBuffer returns a Buffer keeping at most max entries for at most ttl.
func NewBuffer(max int, ttl time.Duration) *Buffer {
	return &Buffer{ttl: ttl, max: max, now: time.Now}
}

// Add appends an alert, assigning it the next sequence number.
func (b *Buffer) Add(a Alert) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	b.items = append(b.items, buffered{seq: b.seq, at: b.now(), alert: a})
	b.prune()
}

// Since returns alerts newer than cursor and the latest sequence number, so the
// caller can advance its cursor. Expired entries are pruned first.
func (b *Buffer) Since(cursor int64) ([]Alert, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prune()

	if cursor > b.seq {
		// The server's sequence counter is behind the caller's cursor, which
		// only happens after a restart (Buffer.seq resets to 0 in memory,
		// but browsers persist their cursor in localStorage). Treat the
		// caller as unseen so alerts buffered since the restart aren't
		// silently skipped.
		cursor = 0
	}

	out := make([]Alert, 0)
	for _, it := range b.items {
		if it.seq > cursor {
			out = append(out, it.alert)
		}
	}
	return out, b.seq
}

// prune drops expired and over-capacity entries. Caller holds the lock.
func (b *Buffer) prune() {
	cutoff := b.now().Add(-b.ttl)
	kept := b.items[:0:0]
	for _, it := range b.items {
		if it.at.After(cutoff) {
			kept = append(kept, it)
		}
	}
	if len(kept) > b.max {
		kept = kept[len(kept)-b.max:]
	}
	b.items = kept
}

// BrowserChannel delivers alerts by appending them to a Buffer.
type BrowserChannel struct {
	buf *Buffer
}

// NewBrowserChannel returns a channel that feeds buf.
func NewBrowserChannel(buf *Buffer) *BrowserChannel {
	return &BrowserChannel{buf: buf}
}

func (c *BrowserChannel) Name() string { return "browser" }

func (c *BrowserChannel) Send(_ context.Context, a Alert) error {
	c.buf.Add(a)
	return nil
}
