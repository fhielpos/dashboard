package core

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Plugin is the interface every dashboard plugin must implement.
type Plugin interface {
	Name() string
	Description() string
	Initialize(ctx context.Context, config map[string]interface{}) error
	Fetch(ctx context.Context) ([]Item, error)
	Shutdown(ctx context.Context) error
}

// WritablePlugin extends Plugin with mutation capabilities (e.g. tasks).
type WritablePlugin interface {
	Plugin
	CreateItem(ctx context.Context, item Item) (Item, error)
	UpdateItem(ctx context.Context, id string, updates map[string]interface{}) (Item, error)
	DeleteItem(ctx context.Context, id string) error
}

// PluginInfo holds runtime metadata for a registered plugin.
type PluginInfo struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Writable    bool       `json:"writable"`
	Enabled     bool       `json:"enabled"`
	LastFetch   *time.Time `json:"last_fetch,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	ItemCount   int        `json:"item_count"`
}

// Registry manages plugin registration, caching, and status tracking.
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]Plugin
	cache   map[string][]Item
	status  map[string]*PluginInfo
}

func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]Plugin),
		cache:   make(map[string][]Item),
		status:  make(map[string]*PluginInfo),
	}
}

// Register adds a plugin to the registry.
func (r *Registry) Register(p Plugin) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, writable := p.(WritablePlugin)
	r.plugins[p.Name()] = p
	r.status[p.Name()] = &PluginInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Writable:    writable,
		Enabled:     true,
	}
}

// Plugin returns a plugin by name.
func (r *Registry) Plugin(name string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[name]
	return p, ok
}

// Refresh fetches fresh items from a single plugin and updates the cache.
func (r *Registry) Refresh(ctx context.Context, name string) error {
	r.mu.RLock()
	p, ok := r.plugins[name]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("plugin %q not found", name)
	}

	items, err := p.Fetch(ctx)
	now := time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.status[name]
	st.LastFetch = &now
	if err != nil {
		st.LastError = err.Error()
		return err
	}
	st.LastError = ""
	st.ItemCount = len(items)
	r.cache[name] = items
	return nil
}

// RefreshAll refreshes all plugins concurrently.
func (r *Registry) RefreshAll(ctx context.Context) map[string]error {
	r.mu.RLock()
	names := make([]string, 0, len(r.plugins))
	for name := range r.plugins {
		names = append(names, name)
	}
	r.mu.RUnlock()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs = make(map[string]error)
	)
	for _, name := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			if err := r.Refresh(ctx, n); err != nil {
				mu.Lock()
				errs[n] = err
				mu.Unlock()
			}
		}(name)
	}
	wg.Wait()
	return errs
}

// Items returns cached items with optional filtering.
func (r *Registry) Items(pluginFilter, typeFilter string) []Item {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []Item
	for name, items := range r.cache {
		if pluginFilter != "" && name != pluginFilter {
			continue
		}
		for _, item := range items {
			if typeFilter != "" && string(item.Type) != typeFilter {
				continue
			}
			result = append(result, item)
		}
	}
	return result
}

// PluginInfos returns status snapshots for all plugins.
func (r *Registry) PluginInfos() []PluginInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]PluginInfo, 0, len(r.status))
	for _, v := range r.status {
		out = append(out, *v)
	}
	return out
}
