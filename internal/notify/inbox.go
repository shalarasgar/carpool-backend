package notify

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shalarasgar/carpool-backend/internal/store"
)

// Inbox stores events in the notifications table. Notify must run on a
// context carrying the transaction queries (store.WithQueries). The payload
// is type plus ids only; phone numbers are never written.
type Inbox struct{}

// Notify implements Notifier.
func (Inbox) Notify(ctx context.Context, events ...Event) error {
	if len(events) == 0 {
		return nil
	}
	q, ok := store.QueriesFromContext(ctx)
	if !ok {
		return fmt.Errorf("notification insert requires a transaction")
	}
	for _, event := range events {
		if event.Kind == "" {
			return fmt.Errorf("notification type is empty")
		}
		var userID pgtype.UUID
		if err := userID.Scan(event.RecipientID); err != nil || !userID.Valid {
			return fmt.Errorf("notification recipient is invalid")
		}
		payload := map[string]string{}
		if event.ActorID != "" {
			payload["actor_id"] = event.ActorID
		}
		if event.TripID != "" {
			payload["trip_id"] = event.TripID
		}
		if event.BookingID != "" {
			payload["booking_id"] = event.BookingID
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("notification payload: %w", err)
		}
		if _, err := q.InsertNotification(ctx, store.InsertNotificationParams{
			UserID:  userID,
			Type:    string(event.Kind),
			Payload: raw,
		}); err != nil {
			return fmt.Errorf("insert notification: %w", err)
		}
	}
	return nil
}
