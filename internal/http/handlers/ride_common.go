package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/auth"
	httperr "github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/i18n"
	"github.com/shalarasgar/carpool-backend/internal/media"
	"github.com/shalarasgar/carpool-backend/internal/ride"
	"github.com/shalarasgar/carpool-backend/internal/smsqueue"
)

var rideErrors = []struct {
	err    error
	status int
	code   string
}{
	{ride.ErrNotFound, http.StatusNotFound, "NOT_FOUND"},
	{ride.ErrForbidden, http.StatusForbidden, "FORBIDDEN"},
	{ride.ErrProfileIncomplete, http.StatusUnprocessableEntity, "PROFILE_INCOMPLETE"},
	{ride.ErrTripOverlap, http.StatusConflict, "TRIP_OVERLAP"},
	{ride.ErrInsufficientSeats, http.StatusConflict, "INSUFFICIENT_SEATS"},
	{ride.ErrTripNotActive, http.StatusConflict, "TRIP_NOT_ACTIVE"},
	{ride.ErrTripStarted, http.StatusConflict, "TRIP_ALREADY_STARTED"},
	{ride.ErrTripNotStarted, http.StatusConflict, "TRIP_NOT_STARTED"},
	{ride.ErrBookingNotPending, http.StatusConflict, "BOOKING_NOT_PENDING"},
	{ride.ErrInvalidSegment, http.StatusUnprocessableEntity, "INVALID_SEGMENT"},
	{ride.ErrBookingExists, http.StatusConflict, "BOOKING_EXISTS"},
	{ride.ErrOwnTrip, http.StatusUnprocessableEntity, "OWN_TRIP"},
	{ride.ErrInvalidTransition, http.StatusConflict, "INVALID_TRANSITION"},
	{ride.ErrReviewNotAllowed, http.StatusUnprocessableEntity, "REVIEW_NOT_ALLOWED"},
	{ride.ErrReviewExists, http.StatusConflict, "REVIEW_EXISTS"},
	{ride.ErrSeatsBelowBooked, http.StatusConflict, "SEATS_BELOW_BOOKED"},
	{ride.ErrTripHasBookings, http.StatusConflict, "TRIP_HAS_BOOKINGS"},
	{ride.ErrPhoneTaken, http.StatusConflict, "PHONE_TAKEN"},
	{ride.ErrLocationInactive, http.StatusUnprocessableEntity, "LOCATION_INACTIVE"},
	{ride.ErrContactUnavailable, http.StatusForbidden, "CONTACT_UNAVAILABLE"},
	{ride.ErrUserBlocked, http.StatusForbidden, "USER_UNAVAILABLE"},
	{ride.ErrTripInProgress, http.StatusConflict, "TRIP_IN_PROGRESS"},
	{media.ErrTooLarge, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE"},
	{media.ErrUnsupported, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA"},
	{auth.ErrInvalidPhone, http.StatusUnprocessableEntity, "INVALID_PHONE"},
	{auth.ErrInvalidCode, http.StatusUnauthorized, "INVALID_CODE"},
	{auth.ErrBlocked, http.StatusForbidden, "USER_BLOCKED"},
	{auth.ErrUnauthorized, http.StatusUnauthorized, "UNAUTHORIZED"},
	{auth.ErrValidation, http.StatusUnprocessableEntity, "VALIDATION_ERROR"},
}

func locale(r *http.Request) string { return auth.Locale(r.Header.Get("Accept-Language")) }

// writeRideErr writes a localized error envelope for product endpoints.
func writeRideErr(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	loc := locale(r)
	var field *ride.FieldError
	if errors.As(err, &field) {
		httperr.WriteJSON(w, http.StatusUnprocessableEntity, httperr.Envelope{Error: httperr.Body{
			Code: "VALIDATION_ERROR", Message: i18n.Message(loc, "VALIDATION_ERROR"),
			Field: field.Field, Details: map[string]string{"reason": field.Reason},
		}})
		return
	}
	var overlap *ride.OverlapError
	if errors.As(err, &overlap) {
		body := httperr.Body{Code: "TRIP_OVERLAP", Message: i18n.Message(loc, "TRIP_OVERLAP")}
		if overlap.TripID != "" {
			body.Details = map[string]string{"trip_id": overlap.TripID}
		}
		httperr.WriteJSON(w, http.StatusConflict, httperr.Envelope{Error: body})
		return
	}
	var limited *smsqueue.RateLimited
	if errors.As(err, &limited) {
		writeErr(w, logger, err)
		return
	}
	for _, m := range rideErrors {
		if errors.Is(err, m.err) {
			httperr.Write(w, m.status, m.code, i18n.Message(loc, m.code))
			return
		}
	}
	if logger != nil {
		logger.Error("request failed", "path", r.URL.Path, "err", err)
	}
	httperr.Write(w, http.StatusInternalServerError, "INTERNAL", i18n.Message(loc, "INTERNAL"))
}

func badJSON(w http.ResponseWriter, r *http.Request) {
	httperr.Write(w, http.StatusBadRequest, "VALIDATION_ERROR", i18n.Message(locale(r), "VALIDATION_ERROR"))
}

func queryInt(r *http.Request, key string, def int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return def
	}
	return v
}

type listResponse[T any] struct {
	Items   []T  `json:"items"`
	HasMore bool `json:"has_more"`
}

func mapList[S, T any](in []S, f func(S) T) []T {
	out := make([]T, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

type locationDTO struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id,omitempty"`
	Type     string `json:"type,omitempty"`
	NameTK   string `json:"name_tk"`
	NameRU   string `json:"name_ru"`
	NameEN   string `json:"name_en"`
}

func toLocation(l ride.Location) locationDTO {
	return locationDTO{ID: l.ID, ParentID: l.ParentID, Type: l.Type, NameTK: l.NameTK, NameRU: l.NameRU, NameEN: l.NameEN}
}

type userSummaryDTO struct {
	ID             string  `json:"id"`
	FirstName      string  `json:"first_name"`
	LastName       string  `json:"last_name"`
	PhotoURL       string  `json:"photo_url,omitempty"`
	RatingAvg      float64 `json:"rating_avg"`
	RatingCount    int     `json:"rating_count"`
	CompletedTrips int     `json:"completed_trips"`
	// Deleted is true for an erased account; its name and photo are empty.
	Deleted bool `json:"deleted,omitempty"`
}

func toSummary(u ride.UserSummary) userSummaryDTO {
	return userSummaryDTO{ID: u.ID, FirstName: u.FirstName, LastName: u.LastName, PhotoURL: u.PhotoURL,
		RatingAvg: u.RatingAvg, RatingCount: u.RatingCount, CompletedTrips: u.TripsAsDriver, Deleted: u.Deleted}
}

type profileDTO struct {
	ID               string    `json:"id"`
	Phone            string    `json:"phone,omitempty"`
	FirstName        string    `json:"first_name"`
	LastName         string    `json:"last_name"`
	Bio              string    `json:"bio,omitempty"`
	PhotoURL         string    `json:"photo_url,omitempty"`
	Locale           string    `json:"locale,omitempty"`
	RatingAvg        float64   `json:"rating_avg"`
	RatingCount      int       `json:"rating_count"`
	TripsAsDriver    int       `json:"trips_as_driver"`
	TripsAsPassenger int       `json:"trips_as_passenger"`
	MemberSince      time.Time `json:"member_since"`
	ProfileComplete  bool      `json:"profile_complete"`
}

func toProfile(p ride.Profile, own bool) profileDTO {
	d := profileDTO{ID: p.ID, FirstName: p.FirstName, LastName: p.LastName, Bio: p.Bio, PhotoURL: p.PhotoURL,
		RatingAvg: p.RatingAvg, RatingCount: p.RatingCount, TripsAsDriver: p.TripsAsDriver,
		TripsAsPassenger: p.TripsAsPassenger, MemberSince: p.CreatedAt, ProfileComplete: p.Complete()}
	if own {
		d.Phone, d.Locale = p.Phone, p.Locale
	}
	return d
}

type vehicleDTO struct {
	ID             string `json:"id"`
	Brand          string `json:"brand"`
	Model          string `json:"model"`
	Color          string `json:"color"`
	Plate          string `json:"plate,omitempty"`
	Year           *int   `json:"year,omitempty"`
	VehicleType    string `json:"vehicle_type,omitempty"`
	PassengerSeats *int   `json:"passenger_seats,omitempty"`
	PhotoURL       string `json:"photo_url,omitempty"`
}

func toVehicle(v ride.Vehicle) vehicleDTO {
	return vehicleDTO{ID: v.ID, Brand: v.Brand, Model: v.Model, Color: v.Color, Plate: v.Plate, Year: v.Year,
		VehicleType: v.Type, PassengerSeats: v.PassengerSeats, PhotoURL: v.PhotoURL}
}

type amenitiesDTO struct {
	AirConditioning string `json:"air_conditioning"`
	AudioSystem     string `json:"audio_system"`
	MusicPolicy     string `json:"music_policy"`
	SmokingPolicy   string `json:"smoking_policy"`
	PetsPolicy      string `json:"pets_policy"`
	LuggageSize     string `json:"luggage_size"`
	ChildSeat       string `json:"child_seat"`
	Conversation    string `json:"conversation"`
}

func (a amenitiesDTO) model() ride.Amenities {
	return ride.Amenities(a)
}

type stopDTO struct {
	Order        int         `json:"order"`
	Location     locationDTO `json:"location"`
	MeetingPoint string      `json:"meeting_point,omitempty"`
	PlannedAt    *time.Time  `json:"planned_at,omitempty"`
	StopType     string      `json:"stop_type"`
}

type matchDTO struct {
	PickupOrder  int `json:"pickup_order"`
	DropoffOrder int `json:"dropoff_order"`
	FreeSeats    int `json:"free_seats"`
}

type viewerDTO struct {
	IsDriver      bool   `json:"is_driver"`
	BookingID     string `json:"booking_id,omitempty"`
	BookingStatus string `json:"booking_status,omitempty"`
}

type tripDTO struct {
	ID              string         `json:"id"`
	Status          string         `json:"status"`
	Driver          userSummaryDTO `json:"driver"`
	Vehicle         *vehicleDTO    `json:"vehicle,omitempty"`
	Stops           []stopDTO      `json:"stops"`
	DepartsAt       time.Time      `json:"departs_at"`
	ArrivesAt       time.Time      `json:"arrives_at"`
	SeatsTotal      int            `json:"seats_total"`
	FreeBySegment   []int          `json:"free_seats_by_segment"`
	Price           *float64       `json:"price"`
	PriceNegotiable bool           `json:"price_negotiable"`
	Currency        string         `json:"currency"`
	Description     string         `json:"description,omitempty"`
	Conditions      string         `json:"conditions,omitempty"`
	Amenities       amenitiesDTO   `json:"amenities"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	Match           *matchDTO      `json:"match,omitempty"`
	Viewer          *viewerDTO     `json:"viewer,omitempty"`
	PendingRequests *int           `json:"pending_requests,omitempty"`
	ApprovedSeats   *int           `json:"approved_seats,omitempty"`
}

func toTrip(t ride.Trip) tripDTO {
	d := tripDTO{
		ID: t.ID, Status: string(t.Status), Driver: toSummary(t.Driver), DepartsAt: t.DepartsAt, ArrivesAt: t.ArrivesAt,
		SeatsTotal: t.SeatsTotal, FreeBySegment: t.FreeBySegment, Price: t.Price, PriceNegotiable: t.PriceNegotiable,
		Currency: t.Currency, Description: t.Description, Conditions: t.Conditions,
		Amenities: amenitiesDTO(t.Amenities), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
	if t.Vehicle != nil {
		v := toVehicle(*t.Vehicle)
		if t.Viewer == nil || !t.Viewer.IsDriver {
			v.ID = ""
		}
		d.Vehicle = &v
	}
	d.Stops = mapList(t.Stops, func(s ride.StopView) stopDTO {
		return stopDTO{Order: s.Order, Location: toLocation(s.Location), MeetingPoint: s.MeetingPoint,
			PlannedAt: s.PlannedAt, StopType: string(s.Type)}
	})
	if d.FreeBySegment == nil {
		d.FreeBySegment = []int{}
	}
	if t.Match != nil {
		d.Match = &matchDTO{PickupOrder: t.Match.PickupOrder, DropoffOrder: t.Match.DropoffOrder, FreeSeats: t.Match.FreeSeats}
	}
	if t.Viewer != nil {
		d.Viewer = &viewerDTO{IsDriver: t.Viewer.IsDriver, BookingID: t.Viewer.BookingID, BookingStatus: string(t.Viewer.BookingStatus)}
		if t.Viewer.IsDriver {
			pending, approved := t.PendingRequests, t.ApprovedSeats
			d.PendingRequests, d.ApprovedSeats = &pending, &approved
		}
	}
	return d
}

type bookingDTO struct {
	ID           string         `json:"id"`
	TripID       string         `json:"trip_id"`
	Status       string         `json:"status"`
	Seats        int            `json:"seats"`
	PickupOrder  int            `json:"pickup_order"`
	DropoffOrder int            `json:"dropoff_order"`
	Luggage      string         `json:"luggage"`
	Message      string         `json:"message,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	DecidedAt    *time.Time     `json:"decided_at,omitempty"`
	CancelledAt  *time.Time     `json:"cancelled_at,omitempty"`
	CompletedAt  *time.Time     `json:"completed_at,omitempty"`
	Passenger    userSummaryDTO `json:"passenger"`
	Trip         *tripDTO       `json:"trip,omitempty"`
	ContactPhone string         `json:"contact_phone,omitempty"`
	Reviewed     bool           `json:"reviewed"`
}

func toBooking(b ride.Booking) bookingDTO {
	d := bookingDTO{ID: b.ID, TripID: b.TripID, Status: string(b.Status), Seats: b.Seats, PickupOrder: b.PickupOrder,
		DropoffOrder: b.DropoffOrder, Luggage: b.Luggage, Message: b.Message, CreatedAt: b.CreatedAt,
		DecidedAt: b.DecidedAt, CancelledAt: b.CancelledAt, CompletedAt: b.CompletedAt,
		Passenger: toSummary(b.Passenger), ContactPhone: b.ContactPhone, Reviewed: b.Reviewed}
	if b.Trip != nil {
		t := toTrip(*b.Trip)
		d.Trip = &t
	}
	return d
}

type reviewDTO struct {
	ID         string         `json:"id"`
	BookingID  string         `json:"booking_id"`
	TripID     string         `json:"trip_id"`
	Author     userSummaryDTO `json:"author"`
	AuthorRole string         `json:"author_role"`
	Rating     int            `json:"rating"`
	Comment    string         `json:"comment,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

func toReview(r ride.Review) reviewDTO {
	return reviewDTO{ID: r.ID, BookingID: r.BookingID, TripID: r.TripID, Author: toSummary(r.Author),
		AuthorRole: r.AuthorRole, Rating: r.Rating, Comment: r.Comment, CreatedAt: r.CreatedAt}
}

type notificationDTO struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	TripID    string          `json:"trip_id,omitempty"`
	BookingID string          `json:"booking_id,omitempty"`
	Actor     *userSummaryDTO `json:"actor,omitempty"`
	From      *locationDTO    `json:"from,omitempty"`
	To        *locationDTO    `json:"to,omitempty"`
	DepartsAt *time.Time      `json:"departs_at,omitempty"`
	Read      bool            `json:"read"`
	CreatedAt time.Time       `json:"created_at"`
}

func toNotification(n ride.Notification) notificationDTO {
	d := notificationDTO{ID: n.ID, Type: string(n.Type), TripID: n.TripID, BookingID: n.BookingID,
		DepartsAt: n.DepartsAt, Read: n.ReadAt != nil, CreatedAt: n.CreatedAt}
	if n.Actor != nil {
		a := toSummary(*n.Actor)
		d.Actor = &a
	}
	if n.From != nil {
		f := toLocation(*n.From)
		d.From = &f
	}
	if n.To != nil {
		t := toLocation(*n.To)
		d.To = &t
	}
	return d
}
