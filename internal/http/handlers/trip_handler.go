package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	httperr "github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/ride"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// Trips serves trip publication, search, details, edits, bookings, reviews, and the inbox.
type Trips struct {
	Logger   *slog.Logger
	Trips    *service.Trips
	Bookings *service.Bookings
}

type stopRequest struct {
	LocationID   string     `json:"location_id"`
	MeetingPoint string     `json:"meeting_point"`
	PlannedAt    *time.Time `json:"planned_at"`
	StopType     string     `json:"stop_type"`
}

type createTripRequest struct {
	VehicleID       string        `json:"vehicle_id"`
	DepartsAt       time.Time     `json:"departs_at"`
	ArrivesAt       time.Time     `json:"arrives_at"`
	SeatsTotal      int           `json:"seats_total"`
	Price           *float64      `json:"price"`
	PriceNegotiable bool          `json:"price_negotiable"`
	Description     string        `json:"description"`
	Conditions      string        `json:"conditions"`
	Amenities       amenitiesDTO  `json:"amenities"`
	Stops           []stopRequest `json:"stops"`
}

// Create serves POST /v1/trips.
func (h Trips) Create(w http.ResponseWriter, r *http.Request) {
	var body createTripRequest
	if !decodeBody(w, r, &body) {
		return
	}
	in := ride.TripInput{
		DriverID: middleware.UserFrom(r.Context()), VehicleID: body.VehicleID,
		DepartsAt: body.DepartsAt, ArrivesAt: body.ArrivesAt, SeatsTotal: body.SeatsTotal,
		Price: body.Price, PriceNegotiable: body.PriceNegotiable, Description: body.Description,
		Conditions: body.Conditions, Amenities: body.Amenities.model(),
	}
	if !ride.ValidID(in.VehicleID) {
		writeRideErr(w, r, h.Logger, &ride.FieldError{Field: "vehicle_id", Reason: "required"})
		return
	}
	for _, s := range body.Stops {
		in.Stops = append(in.Stops, ride.Stop{LocationID: s.LocationID, MeetingPoint: s.MeetingPoint,
			PlannedAt: s.PlannedAt, Type: ride.StopType(s.StopType)})
	}
	t, err := h.Trips.Publish(r.Context(), in)
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusCreated, toTrip(t))
}

// Search serves GET /v1/trips.
func (h Trips) Search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sq := ride.SearchQuery{
		ViewerID: middleware.UserFrom(r.Context()), FromID: q.Get("from"), ToID: q.Get("to"),
		Seats: queryInt(r, "seats", 1), TimeFrom: q.Get("time_from"), TimeTo: q.Get("time_to"),
		AirCond: q.Get("air_conditioning") == "yes", PetsAllowed: q.Get("pets") == "allowed",
		NoSmoking: q.Get("smoking") == "prohibited", SmokingOK: q.Get("smoking") == "allowed",
		ChildSeat: q.Get("child_seat") == "yes", MinLuggage: q.Get("luggage"), Sort: q.Get("sort"),
		Page: queryInt(r, "page", 1), Limit: queryInt(r, "limit", 20),
	}
	if d := q.Get("date"); d != "" {
		date, err := time.ParseInLocation("2006-01-02", d, h.Trips.Location())
		if err != nil {
			writeRideErr(w, r, h.Logger, &ride.FieldError{Field: "date", Reason: "format"})
			return
		}
		sq.Date = &date
	}
	for _, f := range []struct {
		key string
		dst **float64
	}{{"price_min", &sq.PriceMin}, {"price_max", &sq.PriceMax}, {"min_rating", &sq.MinRating}} {
		if v := q.Get(f.key); v != "" {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				writeRideErr(w, r, h.Logger, &ride.FieldError{Field: f.key, Reason: "format"})
				return
			}
			*f.dst = &n
		}
	}
	trips, more, err := h.Trips.Search(r.Context(), sq)
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, listResponse[tripDTO]{Items: mapList(trips, toTrip), HasMore: more})
}

// Get serves GET /v1/trips/{id}.
func (h Trips) Get(w http.ResponseWriter, r *http.Request) {
	t, err := h.Trips.Get(r.Context(), chi.URLParam(r, "id"), middleware.UserFrom(r.Context()))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toTrip(t))
}

type patchTripRequest struct {
	VehicleID       *string           `json:"vehicle_id"`
	DepartsAt       *time.Time        `json:"departs_at"`
	ArrivesAt       *time.Time        `json:"arrives_at"`
	SeatsTotal      *int              `json:"seats_total"`
	Price           *float64          `json:"price"`
	ClearPrice      bool              `json:"clear_price"`
	PriceNegotiable *bool             `json:"price_negotiable"`
	Description     *string           `json:"description"`
	Conditions      *string           `json:"conditions"`
	Amenities       *amenitiesDTO     `json:"amenities"`
	MeetingPoints   map[string]string `json:"meeting_points"`
}

// Patch serves PATCH /v1/trips/{id}.
func (h Trips) Patch(w http.ResponseWriter, r *http.Request) {
	var body patchTripRequest
	if !decodeBody(w, r, &body) {
		return
	}
	p := ride.TripPatch{
		VehicleID: body.VehicleID, DepartsAt: body.DepartsAt, ArrivesAt: body.ArrivesAt, SeatsTotal: body.SeatsTotal,
		Price: body.Price, ClearPrice: body.ClearPrice, PriceNegotiable: body.PriceNegotiable,
		Description: body.Description, Conditions: body.Conditions,
	}
	if body.Amenities != nil {
		a := body.Amenities.model()
		p.Amenities = &a
	}
	if len(body.MeetingPoints) > 0 {
		p.MeetingPoints = map[int]string{}
		for k, v := range body.MeetingPoints {
			order, err := strconv.Atoi(k)
			if err != nil {
				writeRideErr(w, r, h.Logger, &ride.FieldError{Field: "meeting_points", Reason: "unknown_stop"})
				return
			}
			p.MeetingPoints[order] = v
		}
	}
	t, err := h.Trips.Update(r.Context(), middleware.UserFrom(r.Context()), chi.URLParam(r, "id"), p)
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toTrip(t))
}

// Cancel serves POST /v1/trips/{id}/cancel.
func (h Trips) Cancel(w http.ResponseWriter, r *http.Request) {
	t, err := h.Trips.Cancel(r.Context(), middleware.UserFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toTrip(t))
}

// Complete serves POST /v1/trips/{id}/complete.
func (h Trips) Complete(w http.ResponseWriter, r *http.Request) {
	t, err := h.Trips.Complete(r.Context(), middleware.UserFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toTrip(t))
}

// MyTrips serves GET /v1/me/trips?scope=upcoming|history.
func (h Trips) MyTrips(w http.ResponseWriter, r *http.Request) {
	trips, more, err := h.Trips.Mine(r.Context(), middleware.UserFrom(r.Context()), r.URL.Query().Get("scope"),
		queryInt(r, "page", 1), queryInt(r, "limit", 20))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, listResponse[tripDTO]{Items: mapList(trips, toTrip), HasMore: more})
}

type bookingRequest struct {
	PickupOrder  *int   `json:"pickup_order"`
	DropoffOrder *int   `json:"dropoff_order"`
	Seats        int    `json:"seats"`
	Luggage      string `json:"luggage"`
	Message      string `json:"message"`
}

// RequestBooking serves POST /v1/trips/{id}/bookings.
func (h Trips) RequestBooking(w http.ResponseWriter, r *http.Request) {
	var body bookingRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if body.PickupOrder == nil || body.DropoffOrder == nil {
		writeRideErr(w, r, h.Logger, ride.ErrInvalidSegment)
		return
	}
	b, err := h.Bookings.Request(r.Context(), ride.BookingInput{
		PassengerID: middleware.UserFrom(r.Context()), TripID: chi.URLParam(r, "id"),
		PickupOrder: *body.PickupOrder, DropoffOrder: *body.DropoffOrder, Seats: body.Seats,
		Luggage: body.Luggage, Message: body.Message,
	})
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusCreated, toBooking(b))
}

// TripBookings serves GET /v1/trips/{id}/bookings for the driver.
func (h Trips) TripBookings(w http.ResponseWriter, r *http.Request) {
	list, err := h.Bookings.ForTrip(r.Context(), middleware.UserFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, listResponse[bookingDTO]{Items: mapList(list, toBooking)})
}

// MyBookings serves GET /v1/me/bookings?scope=upcoming|history.
func (h Trips) MyBookings(w http.ResponseWriter, r *http.Request) {
	list, more, err := h.Bookings.Mine(r.Context(), middleware.UserFrom(r.Context()), r.URL.Query().Get("scope"),
		queryInt(r, "page", 1), queryInt(r, "limit", 20))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, listResponse[bookingDTO]{Items: mapList(list, toBooking), HasMore: more})
}

// GetBooking serves GET /v1/bookings/{id}.
func (h Trips) GetBooking(w http.ResponseWriter, r *http.Request) {
	b, err := h.Bookings.Get(r.Context(), middleware.UserFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toBooking(b))
}

// Approve serves POST /v1/bookings/{id}/approve.
func (h Trips) Approve(w http.ResponseWriter, r *http.Request) {
	h.bookingAction(w, r, h.Bookings.Approve)
}

// Reject serves POST /v1/bookings/{id}/reject.
func (h Trips) Reject(w http.ResponseWriter, r *http.Request) {
	h.bookingAction(w, r, h.Bookings.Reject)
}

// CancelBooking serves POST /v1/bookings/{id}/cancel.
func (h Trips) CancelBooking(w http.ResponseWriter, r *http.Request) {
	h.bookingAction(w, r, h.Bookings.Cancel)
}

func (h Trips) bookingAction(w http.ResponseWriter, r *http.Request, fn func(ctx context.Context, userID, id string) (ride.Booking, error)) {
	b, err := fn(r.Context(), middleware.UserFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toBooking(b))
}

// Contact serves GET /v1/bookings/{id}/contact. The backend decides who may see a phone.
func (h Trips) Contact(w http.ResponseWriter, r *http.Request) {
	phone, err := h.Bookings.Contact(r.Context(), middleware.UserFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, map[string]string{"phone": phone})
}

type reviewRequest struct {
	Rating  int    `json:"rating"`
	Comment string `json:"comment"`
}

// Review serves POST /v1/bookings/{id}/review.
func (h Trips) Review(w http.ResponseWriter, r *http.Request) {
	var body reviewRequest
	if !decodeBody(w, r, &body) {
		return
	}
	id, err := h.Bookings.Review(r.Context(), ride.ReviewInput{
		BookingID: chi.URLParam(r, "id"), AuthorID: middleware.UserFrom(r.Context()),
		Rating: body.Rating, Comment: body.Comment,
	})
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// UserReviews serves GET /v1/users/{id}/reviews.
func (h Trips) UserReviews(w http.ResponseWriter, r *http.Request) {
	list, more, err := h.Bookings.Reviews(r.Context(), chi.URLParam(r, "id"), queryInt(r, "page", 1), queryInt(r, "limit", 20))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, listResponse[reviewDTO]{Items: mapList(list, toReview), HasMore: more})
}

// Notifications serves GET /v1/me/notifications?before=RFC3339.
func (h Trips) Notifications(w http.ResponseWriter, r *http.Request) {
	var before *time.Time
	if v := r.URL.Query().Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeRideErr(w, r, h.Logger, &ride.FieldError{Field: "before", Reason: "format"})
			return
		}
		before = &t
	}
	userID := middleware.UserFrom(r.Context())
	list, more, err := h.Bookings.Inbox(r.Context(), userID, before, queryInt(r, "limit", 30))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, listResponse[notificationDTO]{Items: mapList(list, toNotification), HasMore: more})
}

// UnreadCount serves GET /v1/me/notifications/unread-count.
func (h Trips) UnreadCount(w http.ResponseWriter, r *http.Request) {
	n, err := h.Bookings.Unread(r.Context(), middleware.UserFrom(r.Context()))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, map[string]int{"unread": n})
}

// MarkRead serves POST /v1/me/notifications/read {ids?: []}. No ids marks all.
func (h Trips) MarkRead(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := h.Bookings.MarkRead(r.Context(), middleware.UserFrom(r.Context()), body.IDs); err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
