// Package notify is the outbound notification port. Phase D calls it when a
// booking or trip changes. The inbox table and any push or SMS delivery
// arrive in Phase E; Nop is the stand-in until then.
package notify

import (
	"context"
	"log/slog"
)

// Kind is a stable event name the future inbox will store.
type Kind string

const (
	KindBookingRequested Kind = "booking_requested"
	KindBookingApproved  Kind = "booking_approved"
	KindBookingRejected  Kind = "booking_rejected"
	KindBookingCancelled Kind = "booking_cancelled"
	KindTripCancelled    Kind = "trip_cancelled"
)

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
// A failure must not be required for the booking change to stay committed;
// callers log and continue.
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
