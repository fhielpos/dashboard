package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/franco/dashboard/internal/core"
)

// Server holds the HTTP server and its dependencies.
type Server struct {
	registry *core.Registry
	httpSrv  *http.Server
}

func NewServer(port int, registry *core.Registry, name, tz, commit string) (*Server, error) {
	s := &Server{registry: registry}

	ui, err := newUIHandler(registry, name, tz, commit)
	if err != nil {
		return nil, err
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	// UI routes
	r.Get("/", ui.handleIndex)
	r.Route("/htmx", func(r chi.Router) {
		r.Get("/calendar", ui.handleCalendar)
		r.Get("/tasks", ui.handleTasks)
		r.Post("/tasks", ui.handleCreateTask)
		r.Post("/tasks/{id}/complete", ui.handleCompleteTask)
		r.Post("/tasks/{id}/important", ui.handleToggleImportant)
		r.Post("/tasks/{id}/delete", ui.handleDeleteTask)
		r.Get("/prs", ui.handlePRs)
		r.Get("/issues", ui.handleIssues)
		r.Get("/notifications", ui.handleNotifications)
		r.Post("/notifications/{id}/dismiss", ui.handleDismissNotification)
		r.Get("/releases", ui.handleReleases)
	})

	r.Get("/health", s.handleHealth)

	r.Route("/api", func(r chi.Router) {
		r.Get("/items", s.handleItems)
		r.Get("/plugins", s.handlePlugins)

		r.Route("/plugins/{plugin}", func(r chi.Router) {
			r.Post("/refresh", s.handleRefresh)
			// Writable plugin endpoints.
			r.Post("/items", s.handleCreateItem)
			r.Patch("/items/{id}", s.handleUpdateItem)
			r.Delete("/items/{id}", s.handleDeleteItem)
		})
	})

	s.httpSrv = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: r,
	}
	return s, nil
}

func (s *Server) Start() error {
	return s.httpSrv.ListenAndServe()
}


func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}

// corsMiddleware adds permissive CORS headers for local development.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
