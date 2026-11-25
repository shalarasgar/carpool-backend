package handlers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	httperr "github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/ride"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// AdminPanel serves the read side of the admin panel: the dashboard, account search, and
// account cards. It is protected by the admin token.
type AdminPanel struct {
	Logger  *slog.Logger
	Service *service.Admin
}

type overviewDTO struct {
	DayStart time.Time `json:"day_start"`
	Reports  struct {
		Open int `json:"open"`
	} `json:"reports"`
	Users struct {
		Total    int `json:"total"`
		NewToday int `json:"new_today"`
		Blocked  int `json:"blocked"`
	} `json:"users"`
	Trips struct {
		Upcoming int `json:"upcoming"`
		NewToday int `json:"new_today"`
	} `json:"trips"`
	Bookings struct {
		Pending  int `json:"pending"`
		NewToday int `json:"new_today"`
	} `json:"bookings"`
	SMS struct {
		Pending         int        `json:"pending"`
		FailedToday     int        `json:"failed_today"`
		GatewaysActive  int        `json:"gateways_active"`
		LastHeartbeatAt *time.Time `json:"last_heartbeat_at"`
	} `json:"sms"`
}

func toOverview(o ride.Overview) overviewDTO {
	var d overviewDTO
	d.DayStart = o.Since
	d.Reports.Open = o.ReportsOpen
	d.Users.Total, d.Users.NewToday, d.Users.Blocked = o.UsersTotal, o.UsersNew, o.UsersBlocked
	d.Trips.Upcoming, d.Trips.NewToday = o.TripsUpcoming, o.TripsNew
	d.Bookings.Pending, d.Bookings.NewToday = o.BookingsPending, o.BookingsNew
	d.SMS.Pending, d.SMS.FailedToday = o.SMSPending, o.SMSFailed
	d.SMS.GatewaysActive, d.SMS.LastHeartbeatAt = o.GatewaysActive, o.LastHeartbeat
	return d
}

type adminUserDTO struct {
	ID          string    `json:"id"`
	Phone       string    `json:"phone,omitempty"`
	FirstName   string    `json:"first_name"`
	LastName    string    `json:"last_name"`
	PhotoURL    string    `json:"photo_url,omitempty"`
	RatingAvg   float64   `json:"rating_avg"`
	RatingCount int       `json:"rating_count"`
	Blocked     bool      `json:"blocked"`
	Deleted     bool      `json:"deleted"`
	CreatedAt   time.Time `json:"created_at"`
	OpenReports int       `json:"open_reports"`
}

func toAdminUser(u ride.AdminUser) adminUserDTO {
	return adminUserDTO{ID: u.ID, Phone: u.Phone, FirstName: u.FirstName, LastName: u.LastName,
		PhotoURL: u.PhotoURL, RatingAvg: u.RatingAvg, RatingCount: u.RatingCount, Blocked: u.Blocked,
		Deleted: u.Deleted, CreatedAt: u.CreatedAt, OpenReports: u.OpenReports}
}

type userActivityDTO struct {
	TripsUpcoming          int `json:"trips_upcoming"`
	TripsCompleted         int `json:"trips_completed"`
	TripsCancelled         int `json:"trips_cancelled"`
	DriverCancellations    int `json:"driver_cancellations"`
	BookingsActive         int `json:"bookings_active"`
	BookingsCompleted      int `json:"bookings_completed"`
	PassengerCancellations int `json:"passenger_cancellations"`
	ReportsAgainst         int `json:"reports_against"`
	ReportsAgainstOpen     int `json:"reports_against_open"`
	Reporters              int `json:"reporters"`
	ReportsFiled           int `json:"reports_filed"`
	BlockedBy              int `json:"blocked_by"`
}

type adminUserDetailDTO struct {
	adminUserDTO
	Bio       string          `json:"bio,omitempty"`
	Locale    string          `json:"locale"`
	DeletedAt *time.Time      `json:"deleted_at,omitempty"`
	Activity  userActivityDTO `json:"activity"`
	Vehicles  []vehicleDTO    `json:"vehicles"`
}

func toAdminUserDetail(d ride.AdminUserDetail) adminUserDetailDTO {
	a := d.Activity
	return adminUserDetailDTO{
		adminUserDTO: toAdminUser(d.AdminUser), Bio: d.Bio, Locale: d.Locale, DeletedAt: d.DeletedAt,
		Activity: userActivityDTO{
			TripsUpcoming: a.TripsUpcoming, TripsCompleted: a.TripsCompleted, TripsCancelled: a.TripsCancelled,
			DriverCancellations: a.DriverCancellations, BookingsActive: a.BookingsActive,
			BookingsCompleted: a.BookingsCompleted, PassengerCancellations: a.PassengerCancellations,
			ReportsAgainst: a.ReportsAgainst, ReportsAgainstOpen: a.ReportsAgainstOpen, Reporters: a.Reporters,
			ReportsFiled: a.ReportsFiled, BlockedBy: a.BlockedBy,
		},
		Vehicles: mapList(d.Vehicles, toVehicle),
	}
}

// Overview serves GET /admin/v1/overview.
func (h AdminPanel) Overview(w http.ResponseWriter, r *http.Request) {
	o, err := h.Service.Overview(r.Context())
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toOverview(o))
}

// Users serves GET /admin/v1/users?q=&status=active|blocked|deleted&page=&limit=.
func (h AdminPanel) Users(w http.ResponseWriter, r *http.Request) {
	q := ride.UserQuery{Text: r.URL.Query().Get("q"), State: ride.UserState(r.URL.Query().Get("status"))}
	list, more, err := h.Service.Users(r.Context(), q, queryInt(r, "page", 1), queryInt(r, "limit", 20))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, listResponse[adminUserDTO]{Items: mapList(list, toAdminUser), HasMore: more})
}

// User serves GET /admin/v1/users/{id}.
func (h AdminPanel) User(w http.ResponseWriter, r *http.Request) {
	d, err := h.Service.User(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toAdminUserDetail(d))
}

// UserTrips serves GET /admin/v1/users/{id}/trips?scope=upcoming|history.
func (h AdminPanel) UserTrips(w http.ResponseWriter, r *http.Request) {
	list, more, err := h.Service.UserTrips(r.Context(), chi.URLParam(r, "id"), r.URL.Query().Get("scope"),
		queryInt(r, "page", 1), queryInt(r, "limit", 20))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, listResponse[tripDTO]{Items: mapList(list, toTrip), HasMore: more})
}

// UserBookings serves GET /admin/v1/users/{id}/bookings?scope=upcoming|history.
func (h AdminPanel) UserBookings(w http.ResponseWriter, r *http.Request) {
	list, more, err := h.Service.UserBookings(r.Context(), chi.URLParam(r, "id"), r.URL.Query().Get("scope"),
		queryInt(r, "page", 1), queryInt(r, "limit", 20))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, listResponse[bookingDTO]{Items: mapList(list, toBooking), HasMore: more})
}

// UserReviews serves GET /admin/v1/users/{id}/reviews.
func (h AdminPanel) UserReviews(w http.ResponseWriter, r *http.Request) {
	list, more, err := h.Service.UserReviews(r.Context(), chi.URLParam(r, "id"), queryInt(r, "page", 1), queryInt(r, "limit", 20))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, listResponse[reviewDTO]{Items: mapList(list, toReview), HasMore: more})
}
