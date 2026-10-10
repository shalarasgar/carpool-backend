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

// Deps are the dependencies of the HTTP server. Auth and Gateway may be nil
// in tests that only exercise health routes; their routes are then skipped.
type Deps struct {
	Logger     *slog.Logger
	Health     *service.Health
	Auth       *service.Auth
	Gateway    *service.Gateway
	AdminToken string
	TrustProxy bool
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
	r.Use(middleware.Locale)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httperr.WriteCode(w, r, http.StatusNotFound, "NOT_FOUND")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httperr.WriteCode(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
	})

	health := handlers.Health{Logger: logger, Checker: deps.Health}
	r.Get("/healthz", health.Live)
	r.Get("/readyz", health.Ready)

	if deps.Auth != nil {
		authH := handlers.Auth{Logger: logger, Service: deps.Auth, TrustProxy: deps.TrustProxy}
		r.Route("/v1/auth", func(r chi.Router) {
			r.Post("/request-otp", authH.RequestOTP)
			r.Post("/verify-otp", authH.VerifyOTP)
			r.Post("/refresh", authH.Refresh)
			r.With(middleware.RequireUser(deps.Auth)).Post("/logout", authH.Logout)
		})
	}

	if deps.Gateway != nil {
		gwH := handlers.Gateway{Logger: logger, Service: deps.Gateway}
		r.Route("/gateway/v1", func(r chi.Router) {
			r.Use(middleware.RequireGateway(deps.Gateway))
			r.Get("/pending", gwH.Pending)
			r.Post("/report", gwH.Report)
			r.Post("/heartbeat", gwH.Heartbeat)
		})
		r.Route("/admin/v1", func(r chi.Router) {
			r.Use(middleware.RequireAdmin(deps.AdminToken))
			r.Post("/gateways", gwH.Create)
		})
	}
	return r
}
