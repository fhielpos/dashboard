package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

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

// handleAlerts returns browser-bound alerts newer than the "since" cursor,
// along with the latest cursor so the client can advance it. It is polled by
// the dashboard, which fires a browser Notification per returned alert.
func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	type alertJSON struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		URL   string `json:"url"`
	}

	if s.alerts == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"alerts": []alertJSON{}, "cursor": 0})
		return
	}

	cursor, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	list, next := s.alerts.Since(cursor)

	out := make([]alertJSON, 0, len(list))
	for _, a := range list {
		out = append(out, alertJSON{Title: a.Headline(), Body: a.Title, URL: a.URL})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"alerts": out, "cursor": next})
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

// handleExport returns all issues and PRs as YAML.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	issues := s.registry.Items("", string(core.ItemTypeIssue))
	prs := s.registry.Items("", string(core.ItemTypePR))

	issueURLs := make([]string, 0, len(issues))
	for _, item := range issues {
		if item.URL != "" {
			issueURLs = append(issueURLs, item.URL)
		}
	}

	prURLs := make([]string, 0, len(prs))
	for _, item := range prs {
		if item.URL != "" {
			prURLs = append(prURLs, item.URL)
		}
	}

	payload := map[string][]string{
		"issues":        issueURLs,
		"pull-requests": prURLs,
	}

	out, err := yaml.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}
