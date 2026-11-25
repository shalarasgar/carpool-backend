// Package server wires the HTTP router and middleware.
package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/http/handlers"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// Deps are the dependencies of the HTTP server.
type Deps struct {
	Logger     *slog.Logger
	Health     *service.Health
	Auth       *service.Auth
	Gateway    *service.Gateway
	AdminToken string
	DevMode    bool
	// TrustProxy takes the client address from X-Forwarded-For. Set it only
	// when the API is reachable through a reverse proxy and nothing else.
	TrustProxy bool

	// Product API. When Profiles is nil the product routes are not mounted.
	JWTSecret string
	Users     middleware.UserChecker
	Profiles  *service.Profiles
	Trips     *service.Trips
	Bookings  *service.Bookings
	// PushTokens mounts device registration. Nil leaves the routes out.
	PushTokens *service.PushTokens
	// Moderation mounts blocks and reports, and their admin routes. Nil leaves them out.
	Moderation *service.Moderation
	// Admin mounts the admin panel's dashboard and account pages. Nil leaves them out.
	Admin *service.Admin
	// Analytics mounts the admin activity report. Nil leaves it out.
	Analytics *service.Analytics
	// Staff mounts panel sign-in, staff accounts and the audit log, and admits staff
	// sessions next to AdminToken. Nil leaves only AdminToken.
	Staff *service.Staff
	// StaffLoginLimiter caps sign-in attempts per client address. Nil uses 10 per 15 min.
	StaffLoginLimiter middleware.Limiter
	// Activity counts the users of authenticated requests (counts only). Nil counts nothing.
	Activity middleware.ActivityRecorder
	Limiter  middleware.Limiter
	MediaDir string
}

// NewRouter returns the application router.
func NewRouter(deps Deps) http.Handler {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	r := chi.NewRouter()
	if deps.TrustProxy {
		r.Use(middleware.ForwardedFor)
	}
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

	if deps.Auth != nil {
		authH := handlers.Auth{Logger: logger, Service: deps.Auth, DevMode: deps.DevMode}
		r.Post("/v1/auth/request-otp", authH.RequestOTP)
		r.Post("/v1/auth/verify-otp", authH.VerifyOTP)
		r.Post("/v1/auth/refresh", authH.Refresh)
		r.Post("/v1/auth/logout", authH.Logout)
	}
	if deps.Gateway != nil {
		gw := handlers.Gateway{Logger: logger, Service: deps.Gateway}
		r.Route("/gateway/v1", func(r chi.Router) {
			r.Use(middleware.GatewayAuth(deps.Gateway))
			r.Get("/pending", gw.Pending)
			r.Post("/report", gw.Report)
			r.Post("/sending", gw.Sending)
			r.Post("/heartbeat", gw.Heartbeat)
		})
	}
	if deps.Gateway != nil || deps.Moderation != nil || deps.Admin != nil || deps.Analytics != nil || deps.Staff != nil {
		r.Route("/admin/v1", func(r chi.Router) {
			// Interfaces stay nil (not typed nil) when there is no staff service.
			var sessions middleware.StaffAuthenticator
			var rec handlers.AuditRecorder
			if deps.Staff != nil {
				sessions, rec = deps.Staff, deps.Staff
				limiter := deps.StaffLoginLimiter
				if limiter == nil {
					limiter = &middleware.WindowLimiter{Max: 10, Window: 15 * time.Minute}
				}
				h := handlers.StaffHandler{Logger: logger, Service: deps.Staff}
				r.With(middleware.LimitByIP(limiter)).Post("/auth/login", h.Login)
			}
			r.Group(func(r chi.Router) {
				r.Use(middleware.StaffAuth(deps.AdminToken, sessions))
				mountAdmin(r, deps, logger, rec)
			})
		})
	}
	if deps.Profiles != nil {
		mountProduct(r, deps, logger)
	}
	return r
}

// mountAdmin registers the panel routes. Moderators get the read pages, reports and
// bans; account deletion, gateways, staff and the audit log need the admin role.
func mountAdmin(r chi.Router, deps Deps, logger *slog.Logger, rec handlers.AuditRecorder) {
	if deps.Staff != nil {
		h := handlers.StaffHandler{Logger: logger, Service: deps.Staff}
		r.Post("/auth/logout", h.Logout)
		r.Get("/me", h.Me)
		r.Post("/me/password", h.ChangePassword)
	}
	users := handlers.AdminUsers{Logger: logger, Moderation: deps.Moderation, Profiles: deps.Profiles, Audit: rec}
	if deps.Moderation != nil {
		r.Get("/reports", users.Reports)
		r.Post("/reports/{id}/resolve", users.ResolveReport)
		r.Post("/users/{id}/block", users.BlockUser)
		r.Post("/users/{id}/unblock", users.UnblockUser)
	}
	if deps.Admin != nil {
		panel := handlers.AdminPanel{Logger: logger, Service: deps.Admin}
		r.Get("/overview", panel.Overview)
		r.Get("/users", panel.Users)
		r.Get("/users/{id}", panel.User)
		r.Get("/users/{id}/trips", panel.UserTrips)
		r.Get("/users/{id}/bookings", panel.UserBookings)
		r.Get("/users/{id}/reviews", panel.UserReviews)
	}
	if deps.Analytics != nil {
		stats := handlers.Analytics{Logger: logger, Service: deps.Analytics}
		r.Get("/analytics/activity", stats.Activity)
	}

	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAdmin)
		if deps.Gateway != nil {
			admin := handlers.Admin{Logger: logger, Service: deps.Gateway, Audit: rec}
			r.Post("/gateways", admin.CreateGateway)
			r.Get("/gateways", admin.ListGateways)
			r.Post("/gateways/{id}/revoke", admin.RevokeGateway)
			r.Get("/sms/stats", admin.Stats)
		}
		if deps.Moderation != nil && deps.Profiles != nil {
			r.Post("/users/delete", users.DeleteAccount)
		}
		if deps.Staff != nil {
			h := handlers.StaffHandler{Logger: logger, Service: deps.Staff}
			r.Get("/staff", h.List)
			r.Post("/staff", h.Create)
			r.Patch("/staff/{id}", h.Update)
			r.Post("/staff/{id}/password", h.ResetPassword)
			r.Get("/audit", h.Audit)
		}
	})
}

func mountProduct(r chi.Router, deps Deps, logger *slog.Logger) {
	me := handlers.Me{Logger: logger, Profiles: deps.Profiles, DevMode: deps.DevMode}
	trips := handlers.Trips{Logger: logger, Trips: deps.Trips, Bookings: deps.Bookings}

	r.Get("/v1/locations", me.Locations)
	if deps.MediaDir != "" {
		files := http.StripPrefix("/media/", http.FileServer(http.Dir(deps.MediaDir)))
		r.Get("/media/*", func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=86400")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			files.ServeHTTP(w, req)
		})
	}

	r.Route("/v1", func(r chi.Router) {
		r.Use(middleware.UserAuth(deps.JWTSecret, deps.Users))
		if deps.Activity != nil {
			r.Use(middleware.TrackActivity(deps.Activity))
		}
		if deps.Limiter != nil {
			r.Use(middleware.RateLimit(deps.Limiter))
		}

		r.Get("/me", me.Get)
		r.Patch("/me", me.Patch)
		r.Delete("/me", me.Delete)
		r.Post("/me/photo", me.UploadPhoto)
		r.Delete("/me/photo", me.DeletePhoto)
		r.Post("/me/phone/request-otp", me.RequestPhoneChange)
		r.Post("/me/phone/verify", me.ConfirmPhoneChange)
		r.Get("/me/vehicles", me.Vehicles)
		r.Post("/me/vehicles", me.AddVehicle)
		r.Put("/me/vehicles/{id}", me.EditVehicle)
		r.Delete("/me/vehicles/{id}", me.DeleteVehicle)
		r.Post("/me/vehicles/{id}/photo", me.VehiclePhoto)
		r.Get("/me/trips", trips.MyTrips)
		r.Get("/me/bookings", trips.MyBookings)
		r.Get("/me/notifications", trips.Notifications)
		r.Get("/me/notifications/unread-count", trips.UnreadCount)
		r.Post("/me/notifications/read", trips.MarkRead)
		if deps.PushTokens != nil {
			push := handlers.Push{Logger: logger, Tokens: deps.PushTokens}
			r.Put("/me/push-token", push.Register)
			r.Delete("/me/push-token", push.Unregister)
		}

		r.Get("/users/{id}", me.User)
		r.Get("/users/{id}/reviews", trips.UserReviews)
		if deps.Moderation != nil {
			mod := handlers.Moderation{Logger: logger, Service: deps.Moderation}
			r.Get("/me/blocks", mod.Blocked)
			r.Post("/users/{id}/block", mod.Block)
			r.Delete("/users/{id}/block", mod.Unblock)
			r.Post("/reports", mod.Report)
		}

		r.Post("/trips", trips.Create)
		r.Get("/trips", trips.Search)
		r.Get("/trips/{id}", trips.Get)
		r.Patch("/trips/{id}", trips.Patch)
		r.Post("/trips/{id}/cancel", trips.Cancel)
		r.Post("/trips/{id}/complete", trips.Complete)
		r.Post("/trips/{id}/bookings", trips.RequestBooking)
		r.Get("/trips/{id}/bookings", trips.TripBookings)

		r.Get("/bookings/{id}", trips.GetBooking)
		r.Post("/bookings/{id}/approve", trips.Approve)
		r.Post("/bookings/{id}/reject", trips.Reject)
		r.Post("/bookings/{id}/cancel", trips.CancelBooking)
		r.Get("/bookings/{id}/contact", trips.Contact)
		r.Post("/bookings/{id}/review", trips.Review)
	})
}
