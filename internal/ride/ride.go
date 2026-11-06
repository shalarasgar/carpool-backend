// Package ride holds the carpool domain: trips, route stops, segment bookings, and the
// rules that decide seat availability and lifecycle transitions.
// It has no HTTP or SQL types, so the store and the tests share one set of rules.
package ride

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

var (
	// ErrNotFound means the resource does not exist or the caller may not know it exists.
	ErrNotFound = errors.New("not found")
	// ErrForbidden means the caller is known but may not perform the action.
	ErrForbidden = errors.New("forbidden")
	// ErrValidation means an input field failed a check. Use *FieldError for details.
	ErrValidation = errors.New("validation")
	// ErrProfileIncomplete means first and last name are required before this action.
	ErrProfileIncomplete = errors.New("profile incomplete")
	// ErrTripOverlap means the driver already has a live trip in that time window.
	ErrTripOverlap = errors.New("trip overlap")
	// ErrInsufficientSeats means a segment of the requested route has too few free seats.
	ErrInsufficientSeats = errors.New("insufficient seats")
	// ErrTripNotActive means the trip is cancelled, completed, or expired.
	ErrTripNotActive = errors.New("trip not active")
	// ErrTripStarted means the trip has already departed.
	ErrTripStarted = errors.New("trip started")
	// ErrTripNotStarted means the trip cannot be completed before departure.
	ErrTripNotStarted = errors.New("trip not started")
	// ErrBookingNotPending means only a pending booking accepts this decision.
	ErrBookingNotPending = errors.New("booking not pending")
	// ErrInvalidSegment means the pickup and drop-off stops do not form a valid route part.
	ErrInvalidSegment = errors.New("invalid segment")
	// ErrBookingExists means the passenger already has a pending or approved booking on the trip.
	ErrBookingExists = errors.New("booking exists")
	// ErrOwnTrip means a driver tried to book their own trip.
	ErrOwnTrip = errors.New("own trip")
	// ErrInvalidTransition means the requested status change is not allowed.
	ErrInvalidTransition = errors.New("invalid transition")
	// ErrReviewNotAllowed means the booking is not completed or the caller did not take part.
	ErrReviewNotAllowed = errors.New("review not allowed")
	// ErrReviewExists means the caller already reviewed this booking.
	ErrReviewExists = errors.New("review exists")
	// ErrSeatsBelowBooked means seats cannot drop below the seats already approved.
	ErrSeatsBelowBooked = errors.New("seats below booked")
	// ErrTripHasBookings means the change needs a trip without pending or approved bookings.
	ErrTripHasBookings = errors.New("trip has bookings")
	// ErrPhoneTaken means another account already uses the phone number.
	ErrPhoneTaken = errors.New("phone taken")
	// ErrLocationInactive means a location is unknown or not enabled for trips.
	ErrLocationInactive = errors.New("location inactive")
	// ErrContactUnavailable means contact details are only shared on an approved booking.
	ErrContactUnavailable = errors.New("contact unavailable")
	// ErrUserBlocked means one of the two users blocked the other.
	ErrUserBlocked = errors.New("user blocked")
)

// FieldError names the field that failed validation. errors.Is(err, ErrValidation) is true.
type FieldError struct {
	Field  string
	Reason string
}

func (e *FieldError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Reason) }

// Is makes a FieldError match ErrValidation.
func (e *FieldError) Is(target error) bool { return target == ErrValidation }

func invalid(field, reason string) error { return &FieldError{Field: field, Reason: reason} }

// OverlapError carries the conflicting trip. errors.Is(err, ErrTripOverlap) is true.
type OverlapError struct {
	TripID string
}

func (e *OverlapError) Error() string { return "trip overlap with " + e.TripID }

// Is makes an OverlapError match ErrTripOverlap.
func (e *OverlapError) Is(target error) bool { return target == ErrTripOverlap }

// TripStatus is the trip lifecycle. "active" is the published state.
type TripStatus string

// Trip statuses.
const (
	TripActive     TripStatus = "active"
	TripFull       TripStatus = "full"
	TripInProgress TripStatus = "in_progress"
	TripCompleted  TripStatus = "completed"
	TripCancelled  TripStatus = "cancelled"
	TripExpired    TripStatus = "expired"
)

// Bookable reports whether new requests and decisions are accepted.
func (s TripStatus) Bookable() bool { return s == TripActive || s == TripFull }

// Live reports whether the trip still occupies the driver's schedule.
func (s TripStatus) Live() bool { return s == TripActive || s == TripFull || s == TripInProgress }

// BookingStatus is the booking lifecycle. "approved" is the accepted state.
type BookingStatus string

// Booking statuses.
const (
	BookingPending              BookingStatus = "pending"
	BookingApproved             BookingStatus = "approved"
	BookingRejected             BookingStatus = "rejected"
	BookingCancelledByPassenger BookingStatus = "cancelled_by_passenger"
	BookingCancelledByDriver    BookingStatus = "cancelled_by_driver"
	BookingCompleted            BookingStatus = "completed"
	BookingExpired              BookingStatus = "expired"
)

// Cancelled reports whether either side cancelled the booking.
func (s BookingStatus) Cancelled() bool {
	return s == BookingCancelledByPassenger || s == BookingCancelledByDriver
}

// Active reports whether the booking still counts as the passenger's request on the trip.
func (s BookingStatus) Active() bool { return s == BookingPending || s == BookingApproved }

// ContactVisible reports whether the two participants may see each other's phone number.
func (s BookingStatus) ContactVisible() bool { return s == BookingApproved || s == BookingCompleted }

var bookingTransitions = map[BookingStatus][]BookingStatus{
	BookingPending: {
		BookingApproved, BookingRejected, BookingCancelledByPassenger, BookingCancelledByDriver, BookingExpired,
	},
	BookingApproved: {BookingCancelledByPassenger, BookingCancelledByDriver, BookingCompleted},
}

// CanTransitionBooking reports whether from -> to is a legal booking change.
// Rejected, cancelled, completed, and expired are terminal.
func CanTransitionBooking(from, to BookingStatus) bool {
	for _, next := range bookingTransitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

var tripTransitions = map[TripStatus][]TripStatus{
	TripActive:     {TripFull, TripInProgress, TripCancelled, TripCompleted, TripExpired},
	TripFull:       {TripActive, TripInProgress, TripCancelled, TripCompleted, TripExpired},
	TripInProgress: {TripCompleted, TripExpired},
}

// CanTransitionTrip reports whether from -> to is a legal trip change.
func CanTransitionTrip(from, to TripStatus) bool {
	for _, next := range tripTransitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

var idPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidID reports whether s is a UUID, the format of every resource id.
func ValidID(s string) bool { return idPattern.MatchString(s) }

// Overlaps reports whether two schedules collide once buffer is added after each trip.
func Overlaps(aStart, aEnd, bStart, bEnd time.Time, buffer time.Duration) bool {
	return aStart.Before(bEnd.Add(buffer)) && bStart.Before(aEnd.Add(buffer))
}
