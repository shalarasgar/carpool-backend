package service

import (
	"context"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// BookingStore is the persistence of bookings, reviews, and the inbox.
type BookingStore interface {
	CreateBooking(ctx context.Context, in ride.BookingInput, now time.Time) (string, error)
	DecideBooking(ctx context.Context, driverID, bookingID string, approve bool, now time.Time) error
	CancelBooking(ctx context.Context, actorID, bookingID string, now time.Time) error
	Booking(ctx context.Context, viewerID, id string) (ride.Booking, error)
	BookingContact(ctx context.Context, viewerID, id string) (string, error)
	TripBookings(ctx context.Context, driverID, tripID string) ([]ride.Booking, error)
	PassengerBookings(ctx context.Context, passengerID, scope string, page, limit int) ([]ride.Booking, bool, error)
	CreateReview(ctx context.Context, in ride.ReviewInput) (string, error)
	UserReviews(ctx context.Context, userID string, page, limit int) ([]ride.Review, bool, error)
	Notifications(ctx context.Context, userID string, before *time.Time, limit int) ([]ride.Notification, bool, error)
	UnreadNotifications(ctx context.Context, userID string) (int, error)
	MarkNotificationsRead(ctx context.Context, userID string, ids []string, now time.Time) error
}

// Bookings handles seat requests, decisions, contact sharing, reviews, and the inbox.
type Bookings struct {
	store BookingStore
	now   func() time.Time
}

// NewBookings returns the booking service.
func NewBookings(store BookingStore) *Bookings {
	return &Bookings{store: store, now: time.Now}
}

// Request creates a pending booking.
func (s *Bookings) Request(ctx context.Context, in ride.BookingInput) (ride.Booking, error) {
	if !ride.ValidID(in.TripID) {
		return ride.Booking{}, ride.ErrNotFound
	}
	if err := in.Normalize(); err != nil {
		return ride.Booking{}, err
	}
	id, err := s.store.CreateBooking(ctx, in, s.now())
	if err != nil {
		return ride.Booking{}, err
	}
	return s.store.Booking(ctx, in.PassengerID, id)
}

// Approve accepts a pending booking if its seats still fit.
func (s *Bookings) Approve(ctx context.Context, driverID, id string) (ride.Booking, error) {
	return s.decide(ctx, driverID, id, true)
}

// Reject declines a pending booking.
func (s *Bookings) Reject(ctx context.Context, driverID, id string) (ride.Booking, error) {
	return s.decide(ctx, driverID, id, false)
}

func (s *Bookings) decide(ctx context.Context, driverID, id string, approve bool) (ride.Booking, error) {
	if !ride.ValidID(id) {
		return ride.Booking{}, ride.ErrNotFound
	}
	if err := s.store.DecideBooking(ctx, driverID, id, approve, s.now()); err != nil {
		return ride.Booking{}, err
	}
	return s.store.Booking(ctx, driverID, id)
}

// Cancel cancels a booking for its passenger or the trip's driver.
func (s *Bookings) Cancel(ctx context.Context, actorID, id string) (ride.Booking, error) {
	if !ride.ValidID(id) {
		return ride.Booking{}, ride.ErrNotFound
	}
	if err := s.store.CancelBooking(ctx, actorID, id, s.now()); err != nil {
		return ride.Booking{}, err
	}
	return s.store.Booking(ctx, actorID, id)
}

// Get returns a booking to one of its participants.
func (s *Bookings) Get(ctx context.Context, viewerID, id string) (ride.Booking, error) {
	if !ride.ValidID(id) {
		return ride.Booking{}, ride.ErrNotFound
	}
	return s.store.Booking(ctx, viewerID, id)
}

// Contact returns the other participant's phone for an approved booking.
func (s *Bookings) Contact(ctx context.Context, viewerID, id string) (string, error) {
	if !ride.ValidID(id) {
		return "", ride.ErrNotFound
	}
	return s.store.BookingContact(ctx, viewerID, id)
}

// ForTrip lists a trip's bookings for its driver.
func (s *Bookings) ForTrip(ctx context.Context, driverID, tripID string) ([]ride.Booking, error) {
	if !ride.ValidID(tripID) {
		return nil, ride.ErrNotFound
	}
	return s.store.TripBookings(ctx, driverID, tripID)
}

// Mine lists the passenger's bookings. scope is "upcoming" or "history".
func (s *Bookings) Mine(ctx context.Context, passengerID, scope string, page, limit int) ([]ride.Booking, bool, error) {
	page, limit = pageBounds(page, limit)
	return s.store.PassengerBookings(ctx, passengerID, scope, page, limit)
}

// Review rates the other participant of a completed booking.
func (s *Bookings) Review(ctx context.Context, in ride.ReviewInput) (string, error) {
	if !ride.ValidID(in.BookingID) {
		return "", ride.ErrNotFound
	}
	if err := in.Normalize(); err != nil {
		return "", err
	}
	return s.store.CreateReview(ctx, in)
}

// Reviews lists the reviews a user received.
func (s *Bookings) Reviews(ctx context.Context, userID string, page, limit int) ([]ride.Review, bool, error) {
	if !ride.ValidID(userID) {
		return nil, false, ride.ErrNotFound
	}
	page, limit = pageBounds(page, limit)
	return s.store.UserReviews(ctx, userID, page, limit)
}

// Inbox lists notifications, newest first.
func (s *Bookings) Inbox(ctx context.Context, userID string, before *time.Time, limit int) ([]ride.Notification, bool, error) {
	_, limit = pageBounds(1, limit)
	return s.store.Notifications(ctx, userID, before, limit)
}

// Unread counts unread notifications.
func (s *Bookings) Unread(ctx context.Context, userID string) (int, error) {
	return s.store.UnreadNotifications(ctx, userID)
}

// MarkRead marks notifications read. Empty ids marks all.
func (s *Bookings) MarkRead(ctx context.Context, userID string, ids []string) error {
	for _, id := range ids {
		if !ride.ValidID(id) {
			return &ride.FieldError{Field: "ids", Reason: "format"}
		}
	}
	if ids == nil {
		ids = []string{}
	}
	return s.store.MarkNotificationsRead(ctx, userID, ids, s.now())
}
