package api

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/franco/dashboard/internal/core"
)

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleItems returns all cached items with optional filtering.
//
// Query params:
//   - plugin: filter by plugin name
//   - type:   filter by item type (pr, issue, event, task)
//   - sort:   "due_at" (default) or "priority"
func (s *Server) handleItems(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items := s.registry.Items(q.Get("plugin"), q.Get("type"))

	switch q.Get("sort") {
	case "priority":
		sort.Slice(items, func(i, j int) bool {
			return items[i].Priority > items[j].Priority
		})
	default: // due_at
		sort.Slice(items, func(i, j int) bool {
			ai := items[i].DueAt
			aj := items[j].DueAt
			if ai == nil && aj == nil {
				return false
			}
			if ai == nil {
				return false
			}
			if aj == nil {
				return true
			}
			return ai.Before(*aj)
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items": items,
		"count": len(items),
	})
}

// handlePlugins lists all registered plugins with their status.
func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	infos := s.registry.PluginInfos()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"plugins": infos,
		"count":   len(infos),
	})
}

// handleRefresh triggers an immediate refresh of the named plugin.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "plugin")
	if err := s.registry.Refresh(r.Context(), name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "refreshed", "plugin": name})
}

// handleCreateItem creates a new item in a writable plugin.
func (s *Server) handleCreateItem(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "plugin")
	p, ok := s.registry.Plugin(name)
	if !ok {
		writeError(w, http.StatusNotFound, "plugin not found")
		return
	}
	wp, ok := p.(core.WritablePlugin)
	if !ok {
		writeError(w, http.StatusMethodNotAllowed, "plugin does not support item creation")
		return
	}

	var item core.Item
	if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	created, err := wp.CreateItem(r.Context(), item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Refresh cache so the new item appears in /api/items.
	_ = s.registry.Refresh(r.Context(), name)

	writeJSON(w, http.StatusCreated, created)
}

// handleUpdateItem patches an item in a writable plugin.
func (s *Server) handleUpdateItem(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "plugin")
	id := chi.URLParam(r, "id")

	p, ok := s.registry.Plugin(name)
	if !ok {
		writeError(w, http.StatusNotFound, "plugin not found")
		return
	}
	wp, ok := p.(core.WritablePlugin)
	if !ok {
		writeError(w, http.StatusMethodNotAllowed, "plugin does not support item updates")
		return
	}

	var updates map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updated, err := wp.UpdateItem(r.Context(), id, updates)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_ = s.registry.Refresh(r.Context(), name)
	writeJSON(w, http.StatusOK, updated)
}

// handleDeleteItem removes an item from a writable plugin.
func (s *Server) handleDeleteItem(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "plugin")
	id := chi.URLParam(r, "id")

	p, ok := s.registry.Plugin(name)
	if !ok {
		writeError(w, http.StatusNotFound, "plugin not found")
		return
	}
	wp, ok := p.(core.WritablePlugin)
	if !ok {
		writeError(w, http.StatusMethodNotAllowed, "plugin does not support item deletion")
		return
	}

	if err := wp.DeleteItem(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_ = s.registry.Refresh(r.Context(), name)
	w.WriteHeader(http.StatusNoContent)
}
