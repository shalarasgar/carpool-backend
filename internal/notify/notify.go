// Package notify is the outbound notification port. Inbox writes the in-app
// inbox inside the caller's transaction. Nop discards events and remains for
// tests that do not assert on the inbox. Push and SMS delivery are not part
// of this package.
package notify

import (
	"context"
	"log/slog"
)

// Kind is a stable inbox event name.
type Kind string

const (
	KindBookingRequested Kind = "booking_requested"
	KindBookingApproved  Kind = "booking_approved"
	KindBookingRejected  Kind = "booking_rejected"
	KindBookingCancelled Kind = "booking_cancelled"
	KindTripCancelled    Kind = "trip_cancelled"
)

// AllKinds lists every inbox event type the API exposes.
func AllKinds() []Kind {
	return []Kind{
		KindBookingRequested,
		KindBookingApproved,
		KindBookingRejected,
		KindBookingCancelled,
		KindTripCancelled,
	}
}

// Event is one notification for one recipient. IDs are user, trip and booking
// UUIDs. Phone numbers are never included.
type Event struct {
	Kind        Kind
	RecipientID string
	ActorID     string
	TripID      string
	BookingID   string
}

// Notifier delivers events. Implementations must be safe for concurrent use.
// Inbox requires store.WithQueries so the insert commits or rolls back with
// the booking or trip change. A Notify error must abort that transaction.
type Notifier interface {
	Notify(ctx context.Context, events ...Event) error
}

// Nop discards events. It logs kinds and ids at debug level and never a phone
// number.
type Nop struct {
	Logger *slog.Logger
}

// Notify implements Notifier.
func (n Nop) Notify(_ context.Context, events ...Event) error {
	logger := n.Logger
	if logger == nil {
		return nil
	}
	for _, event := range events {
		logger.Debug("notification",
			"kind", event.Kind,
			"recipient_id", event.RecipientID,
			"actor_id", event.ActorID,
			"trip_id", event.TripID,
			"booking_id", event.BookingID,
		)
	}
	return nil
}
