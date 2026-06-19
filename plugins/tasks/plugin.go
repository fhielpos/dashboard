package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/franco/dashboard/internal/core"
)

// Plugin provides local task/reminder management with JSON persistence.
type Plugin struct {
	mu          sync.RWMutex
	storagePath string
	tasks       map[string]core.Item
}

func New() *Plugin {
	return &Plugin{
		tasks: make(map[string]core.Item),
	}
}

func (p *Plugin) Name() string        { return "tasks" }
func (p *Plugin) Description() string { return "Local tasks and reminders" }

func (p *Plugin) Initialize(_ context.Context, config map[string]interface{}) error {
	path, _ := config["storage_path"].(string)
	if path == "" {
		path = "/data/tasks.json"
	}
	p.storagePath = path
	return p.load()
}

func (p *Plugin) Fetch(_ context.Context) ([]core.Item, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	items := make([]core.Item, 0, len(p.tasks))
	for _, t := range p.tasks {
		items = append(items, t)
	}
	return items, nil
}

func (p *Plugin) Shutdown(_ context.Context) error {
	return p.save()
}

// CreateItem adds a new task.
func (p *Plugin) CreateItem(_ context.Context, item core.Item) (core.Item, error) {
	item.ID = fmt.Sprintf("task:%d", time.Now().UnixNano())
	item.Plugin = p.Name()
	item.Type = core.ItemTypeTask
	item.FetchedAt = time.Now()
	if item.Status == "" {
		item.Status = "open"
	}

	p.mu.Lock()
	p.tasks[item.ID] = item
	p.mu.Unlock()

	return item, p.save()
}

// UpdateItem patches fields on an existing task.
func (p *Plugin) UpdateItem(_ context.Context, id string, updates map[string]interface{}) (core.Item, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	item, ok := p.tasks[id]
	if !ok {
		return core.Item{}, fmt.Errorf("task %q not found", id)
	}

	if v, ok := updates["title"].(string); ok {
		item.Title = v
	}
	if v, ok := updates["description"].(string); ok {
		item.Description = v
	}
	if v, ok := updates["status"].(string); ok {
		item.Status = v
		if v == "done" || v == "completed" {
			if item.Metadata == nil {
				item.Metadata = make(map[string]interface{})
			}
			item.Metadata["completed_at"] = time.Now().UTC().Format(time.RFC3339)
		} else {
			if item.Metadata != nil {
				delete(item.Metadata, "completed_at")
			}
		}
	}
	if v, ok := updates["priority"].(float64); ok {
		item.Priority = int(v)
	}
	if v, ok := updates["due_at"].(string); ok && v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return core.Item{}, fmt.Errorf("invalid due_at: %w", err)
		}
		item.DueAt = &t
	}
	if v, ok := updates["important"].(bool); ok {
		if item.Metadata == nil {
			item.Metadata = make(map[string]interface{})
		}
		if v {
			item.Metadata["important"] = true
		} else {
			delete(item.Metadata, "important")
		}
	}
	if v, ok := updates["tags"].([]interface{}); ok {
		tags := make([]string, 0, len(v))
		for _, tag := range v {
			if s, ok := tag.(string); ok {
				tags = append(tags, s)
			}
		}
		item.Tags = tags
	}

	p.tasks[id] = item
	return item, p.save()
}

// DeleteItem removes a task.
func (p *Plugin) DeleteItem(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.tasks[id]; !ok {
		return fmt.Errorf("task %q not found", id)
	}
	delete(p.tasks, id)
	return p.save()
}

func (p *Plugin) load() error {
	data, err := os.ReadFile(p.storagePath)
	if os.IsNotExist(err) {
		return nil // first run
	}
	if err != nil {
		return fmt.Errorf("tasks: reading storage: %w", err)
	}

	var items []core.Item
	if err := json.Unmarshal(data, &items); err != nil {
		return fmt.Errorf("tasks: parsing storage: %w", err)
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)

	p.mu.Lock()
	defer p.mu.Unlock()
	for _, item := range items {
		isDone := item.Status == "done" || item.Status == "completed"
		if isDone {
			completedStr, _ := item.Metadata["completed_at"].(string)
			t, err := time.Parse(time.RFC3339, completedStr)
			if err != nil || t.UTC().Truncate(24*time.Hour).Before(today) {
				continue // drop tasks completed on a previous day
			}
		}
		p.tasks[item.ID] = item
	}
	return nil
}

func (p *Plugin) save() error {
	items := make([]core.Item, 0, len(p.tasks))
	for _, t := range p.tasks {
		items = append(items, t)
	}

	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dirOf(p.storagePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p.storagePath, data, 0o644)
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}
