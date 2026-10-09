// Package server wires the HTTP router and middleware.
package server

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/http/handlers"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// Deps are the dependencies of the HTTP server.
type Deps struct {
	Logger *slog.Logger
	Health *service.Health
}

// NewRouter returns the application router.
func NewRouter(deps Deps) http.Handler {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger(logger))
	r.Use(middleware.Recover(logger))

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		httperr.Write(w, http.StatusNotFound, "NOT_FOUND", "Resource not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		httperr.Write(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed")
	})

	health := handlers.Health{Logger: logger, Checker: deps.Health}
	r.Get("/healthz", health.Live)
	r.Get("/readyz", health.Ready)
	return r
}
