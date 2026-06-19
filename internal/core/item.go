package core

import "time"

// ItemType categorizes dashboard items across all plugins.
type ItemType string

const (
	ItemTypePR      ItemType = "pr"
	ItemTypeIssue   ItemType = "issue"
	ItemTypeEvent   ItemType = "event"
	ItemTypeTask    ItemType = "task"
	ItemTypeRelease ItemType = "release"
	ItemTypeStatus  ItemType = "status"
)

// Priority constants.
const (
	PriorityLow    = 0
	PriorityNormal = 1
	PriorityHigh   = 2
	PriorityUrgent = 3
)

// Item is the universal dashboard entry returned by every plugin.
type Item struct {
	ID          string                 `json:"id"`
	Plugin      string                 `json:"plugin"`
	Type        ItemType               `json:"type"`
	Title       string                 `json:"title"`
	Description string                 `json:"description,omitempty"`
	URL         string                 `json:"url,omitempty"`
	DueAt       *time.Time             `json:"due_at,omitempty"`
	StartAt     *time.Time             `json:"start_at,omitempty"`
	Priority    int                    `json:"priority"`
	Status      string                 `json:"status,omitempty"`
	Tags        []string               `json:"tags,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	FetchedAt   time.Time              `json:"fetched_at"`
}
