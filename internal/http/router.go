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
	"github.com/shalarasgar/carpool-backend/internal/storage"
)

// Deps are the dependencies of the HTTP server. Auth, Gateway, Users and
// Locations may be nil in tests that only exercise health routes; their
// routes are then skipped.
type Deps struct {
	Logger        *slog.Logger
	Health        *service.Health
	Auth          *service.Auth
	Gateway       *service.Gateway
	Users         *service.Users
	Vehicles      *service.Vehicles
	Locations     *service.Locations
	Trips         *service.Trips
	Bookings      *service.Bookings
	Reviews       *service.Reviews
	Blocks        *service.Blocks
	Reports       *service.Reports
	Notifications *service.Notifications
	Admin         *service.Admin
	Photos        storage.Store
	PhotoMax      int64
	AdminToken    string
	TrustProxy    bool
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

	if deps.Locations != nil {
		locH := handlers.Locations{Logger: logger, Service: deps.Locations}
		r.Get("/v1/locations", locH.List)
	}
	if deps.Photos != nil {
		media := handlers.Media{Store: deps.Photos}
		r.Get("/v1/media/photos/{name}", media.Photo)
	}
	if deps.Auth != nil && deps.Users != nil && deps.Vehicles != nil {
		usersH := handlers.Users{Logger: logger, Service: deps.Users, MaxPhoto: deps.PhotoMax}
		vehH := handlers.Vehicles{Logger: logger, Service: deps.Vehicles}
		r.Route("/v1", func(r chi.Router) {
			r.Use(middleware.RequireUser(deps.Auth))
			r.Get("/me", usersH.Get)
			r.Patch("/me", usersH.Patch)
			r.Delete("/me", usersH.Delete)
			r.Post("/me/photo", usersH.Photo)
			r.Get("/users/{id}", usersH.Public)
			r.Get("/me/vehicles", vehH.List)
			r.Post("/me/vehicles", vehH.Create)
			r.Patch("/me/vehicles/{id}", vehH.Patch)
			r.Delete("/me/vehicles/{id}", vehH.Delete)
			if deps.Trips != nil && deps.Bookings != nil {
				tripsH := handlers.Trips{Logger: logger, Service: deps.Trips}
				bookH := handlers.Bookings{Logger: logger, Service: deps.Bookings}
				r.Get("/me/trips", tripsH.Mine)
				r.Get("/me/bookings", bookH.Mine)
				r.Get("/trips", tripsH.List)
				r.Post("/trips", tripsH.Create)
				r.Get("/trips/{id}", tripsH.Get)
				r.Patch("/trips/{id}", tripsH.Patch)
				r.Post("/trips/{id}/cancel", tripsH.Cancel)
				r.Post("/trips/{id}/complete", tripsH.Complete)
				r.Post("/trips/{id}/bookings", bookH.Create)
				r.Get("/trips/{id}/bookings", bookH.ForTrip)
				r.Post("/bookings/{id}/approve", bookH.Approve)
				r.Post("/bookings/{id}/reject", bookH.Reject)
				r.Post("/bookings/{id}/cancel", bookH.Cancel)
			}
			if deps.Reviews != nil {
				revH := handlers.Reviews{Logger: logger, Service: deps.Reviews}
				r.Post("/trips/{id}/reviews", revH.Create)
				r.Get("/users/{id}/reviews", revH.List)
			}
			if deps.Blocks != nil {
				blockH := handlers.Blocks{Logger: logger, Service: deps.Blocks}
				r.Get("/me/blocks", blockH.List)
				r.Post("/users/{id}/block", blockH.Create)
				r.Delete("/users/{id}/block", blockH.Delete)
			}
			if deps.Reports != nil {
				reportH := handlers.Reports{Logger: logger, Service: deps.Reports}
				r.Post("/reports", reportH.Create)
			}
			if deps.Notifications != nil {
				noteH := handlers.Notifications{Logger: logger, Service: deps.Notifications}
				r.Get("/me/notifications", noteH.List)
				r.Post("/me/notifications/read", noteH.Read)
			}
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
			if deps.Admin != nil {
				adminH := handlers.Admin{Logger: logger, Service: deps.Admin}
				r.Get("/gateways", adminH.Gateways)
				r.Get("/sms/stats", adminH.SMSStats)
				r.Get("/reports", adminH.Reports)
				r.Patch("/reports/{id}", adminH.UpdateReport)
				r.Post("/users/{id}/block", adminH.BlockUser)
				r.Delete("/users/{id}/block", adminH.UnblockUser)
				r.Patch("/locations/{id}", adminH.SetLocation)
			}
		})
	}
	return r
}
