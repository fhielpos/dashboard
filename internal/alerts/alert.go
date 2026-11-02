// Package alerts turns newly-appeared dashboard items into push notifications
// delivered through one or more channels (ntfy, browser).
package alerts

import (
	"context"
	"time"
)

// Alert is a single push-worthy event derived from a dashboard item.
type Alert struct {
	ID        string
	Title     string
	URL       string
	Repo      string
	Author    string
	Reason    string
	UpdatedAt time.Time
}

// Headline is a short human summary like "Review requested · o/r", used as the
// notification title across channels.
func (a Alert) Headline() string {
	head := reasonText(a.Reason)
	if a.Repo != "" {
		return head + " · " + a.Repo
	}
	return head
}

// Channel delivers an Alert to a destination (ntfy, browser, ...).
type Channel interface {
	Name() string
	Send(ctx context.Context, a Alert) error
}

// Rules toggles which kinds of items generate alerts.
type Rules struct {
	Notifications  bool
	ReviewRequests bool
	AssignedIssues bool
}

// Config holds alerting behaviour independent of delivery channels.
type Config struct {
	Rules          Rules
	ExcludeAuthors []string
	ExcludeRepos   []string
	// CacheTTL is how long a dispatched item is remembered after it stops
	// appearing in a snapshot, so a brief absence (e.g. paginated out, or
	// GitHub's read flag flapping) doesn't make it look "new" again on
	// reappearance. Defaults to DefaultCacheTTL when zero.
	CacheTTL time.Duration
}
