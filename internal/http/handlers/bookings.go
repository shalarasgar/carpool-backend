package handlers

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/shalarasgar/carpool-backend/internal/http/dto"
	httperr "github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/i18n"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// Bookings serves seat requests.
type Bookings struct {
	Logger  *slog.Logger
	Service *service.Bookings
}

// Create handles POST /v1/trips/{id}/bookings.
func (h Bookings) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateBookingRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	booking, err := h.Service.Create(r.Context(), middleware.UserIDFrom(r.Context()), chi.URLParam(r, "id"), req.Seats)
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusCreated, toBookingDTO(booking))
}

// Mine handles GET /v1/me/bookings.
func (h Bookings) Mine(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := h.Service.ListMine(r.Context(), middleware.UserIDFrom(r.Context()), service.PageQuery{
		Page:     q.Get("page"),
		PageSize: q.Get("page_size"),
		Locale:   i18n.FromContext(r.Context()),
	})
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toBookingPage(page))
}

// ForTrip handles GET /v1/trips/{id}/bookings.
func (h Bookings) ForTrip(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := h.Service.ListForTrip(r.Context(), middleware.UserIDFrom(r.Context()), chi.URLParam(r, "id"), service.PageQuery{
		Page:     q.Get("page"),
		PageSize: q.Get("page_size"),
	})
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toBookingPage(page))
}

// Approve handles POST /v1/bookings/{id}/approve.
func (h Bookings) Approve(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, h.Service.Approve)
}

// Reject handles POST /v1/bookings/{id}/reject.
func (h Bookings) Reject(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, h.Service.Reject)
}

// Cancel handles POST /v1/bookings/{id}/cancel.
func (h Bookings) Cancel(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, h.Service.Cancel)
}

func (h Bookings) decide(w http.ResponseWriter, r *http.Request, fn func(context.Context, string, string) (service.Booking, error)) {
	booking, err := fn(r.Context(), middleware.UserIDFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toBookingDTO(booking))
}

func toBookingPage(page service.BookingPage) dto.BookingPage {
	out := dto.BookingPage{
		Bookings: make([]dto.Booking, 0, len(page.Bookings)),
		Page:     page.Page,
		PageSize: page.PageSize,
		HasMore:  page.HasMore,
	}
	for _, booking := range page.Bookings {
		out.Bookings = append(out.Bookings, toBookingDTO(booking))
	}
	return out
}

func toBookingDTO(booking service.Booking) dto.Booking {
	out := dto.Booking{
		ID:     booking.ID,
		TripID: booking.TripID,
		Seats:  booking.Seats,
		Status: booking.Status,
		Passenger: dto.BookingPassenger{
			ID:       booking.Passenger.ID,
			Name:     booking.Passenger.Name,
			PhotoURL: booking.Passenger.PhotoURL,
			Phone:    booking.Passenger.Phone,
		},
		DriverPhone: booking.DriverPhone,
		CreatedAt:   booking.CreatedAt,
		UpdatedAt:   booking.UpdatedAt,
	}
	if booking.Trip != nil {
		trip := toTripDTO(*booking.Trip)
		out.Trip = &trip
	}
	return out
}
