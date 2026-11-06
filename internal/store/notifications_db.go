package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// insertEvents writes inbox rows in the caller's transaction, so a notification exists
// exactly when the change that caused it commits. A repeated event is ignored.
func insertEvents(ctx context.Context, q dbtx, events []ride.Event) error {
	for _, e := range events {
		if e.UserID == "" {
			continue
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO notifications (user_id, type, trip_id, booking_id, actor_id, dedupe_key)
			VALUES ($1::uuid, $2, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, NULLIF($5, '')::uuid, $6)
			ON CONFLICT (user_id, dedupe_key) DO NOTHING
		`, e.UserID, string(e.Type), e.TripID, e.BookingID, e.ActorID, e.DedupeKey); err != nil {
			return dbErr("insert notification", err)
		}
	}
	return nil
}

// notifyPassengers notifies every passenger of the trip whose booking has one of statuses.
func notifyPassengers(ctx context.Context, tx pgx.Tx, tripID string, typ ride.NotificationType, actorID, key string, statuses []ride.BookingStatus) error {
	names := make([]string, len(statuses))
	for i, s := range statuses {
		names[i] = string(s)
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, passenger_id::text FROM bookings WHERE trip_id = $1::uuid AND status::text = ANY($2)
	`, tripID, names)
	if err != nil {
		return dbErr("trip passengers", err)
	}
	var events []ride.Event
	for rows.Next() {
		var bookingID, passenger string
		if err := rows.Scan(&bookingID, &passenger); err != nil {
			rows.Close()
			return dbErr("scan passenger", err)
		}
		events = append(events, ride.Event{
			UserID: passenger, Type: typ, TripID: tripID, BookingID: bookingID, ActorID: actorID, DedupeKey: key,
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return dbErr("passenger rows", err)
	}
	return insertEvents(ctx, tx, events)
}

// Notifications lists a user's inbox, newest first. before pages backwards in time.
func (db *AppDB) Notifications(ctx context.Context, userID string, before *time.Time, limit int) ([]ride.Notification, bool, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT n.id::text, n.type, COALESCE(n.trip_id::text, ''), COALESCE(n.booking_id::text, ''), n.read_at, n.created_at,
		       t.departs_at,
		       fl.id::text, fl.name_tk, fl.name_ru, fl.name_en,
		       tl.id::text, tl.name_tk, tl.name_ru, tl.name_en,
		       a.id::text, a.first_name, a.last_name, a.photo_url
		FROM notifications n
		LEFT JOIN trips t ON t.id = n.trip_id
		LEFT JOIN locations fl ON fl.id = t.from_location_id
		LEFT JOIN locations tl ON tl.id = t.to_location_id
		LEFT JOIN users a ON a.id = n.actor_id
		WHERE n.user_id = $1::uuid AND ($2::timestamptz IS NULL OR n.created_at < $2)
		ORDER BY n.created_at DESC
		LIMIT $3
	`, userID, optTime(before), limit+1)
	if err != nil {
		return nil, false, dbErr("notifications", err)
	}
	defer rows.Close()
	out := []ride.Notification{}
	for rows.Next() {
		var n ride.Notification
		var typ string
		var flID, flTK, flRU, flEN, tlID, tlTK, tlRU, tlEN, aID, aFirst, aLast, aPhoto *string
		if err := rows.Scan(&n.ID, &typ, &n.TripID, &n.BookingID, &n.ReadAt, &n.CreatedAt, &n.DepartsAt,
			&flID, &flTK, &flRU, &flEN, &tlID, &tlTK, &tlRU, &tlEN, &aID, &aFirst, &aLast, &aPhoto); err != nil {
			return nil, false, dbErr("scan notification", err)
		}
		n.Type = ride.NotificationType(typ)
		if flID != nil {
			n.From = &ride.Location{ID: *flID, NameTK: deref(flTK), NameRU: deref(flRU), NameEN: deref(flEN)}
		}
		if tlID != nil {
			n.To = &ride.Location{ID: *tlID, NameTK: deref(tlTK), NameRU: deref(tlRU), NameEN: deref(tlEN)}
		}
		if aID != nil {
			n.Actor = &ride.UserSummary{ID: *aID, FirstName: deref(aFirst), LastName: deref(aLast), PhotoURL: deref(aPhoto)}
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, false, dbErr("notification rows", err)
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}

// UnreadNotifications counts unread inbox rows.
func (db *AppDB) UnreadNotifications(ctx context.Context, userID string) (int, error) {
	var n int
	if err := db.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM notifications WHERE user_id = $1::uuid AND read_at IS NULL
	`, userID).Scan(&n); err != nil {
		return 0, dbErr("unread notifications", err)
	}
	return n, nil
}

// MarkNotificationsRead marks the given ids, or every unread row when ids is empty.
func (db *AppDB) MarkNotificationsRead(ctx context.Context, userID string, ids []string, now time.Time) error {
	if _, err := db.pool.Exec(ctx, `
		UPDATE notifications SET read_at = $3
		WHERE user_id = $1::uuid AND read_at IS NULL AND (cardinality($2::uuid[]) = 0 OR id = ANY($2::uuid[]))
	`, userID, ids, now); err != nil {
		return dbErr("mark read", err)
	}
	return nil
}

// TripLifecycle advances trips whose time has come. Each step locks a batch of due trips
// with SKIP LOCKED, so a trip busy in a booking transaction is retried on the next run.
//   - a bookable trip that departed becomes in_progress and its pending bookings expire;
//   - an in-progress trip past arrival plus grace becomes completed (or expired when it
//     never had an approved passenger);
//   - drivers and approved passengers get one reminder before departure.
func (db *AppDB) TripLifecycle(ctx context.Context, now time.Time, grace, remindBefore time.Duration) error {
	if err := db.eachDueTrip(ctx, `
		SELECT id::text FROM trips WHERE status IN ('active', 'full') AND departs_at <= $1
		ORDER BY departs_at LIMIT 100 FOR UPDATE SKIP LOCKED
	`, []any{now}, func(tx pgx.Tx, id string) error {
		if _, err := tx.Exec(ctx, `UPDATE trips SET status = 'in_progress' WHERE id = $1::uuid`, id); err != nil {
			return dbErr("start trip", err)
		}
		return expirePending(ctx, tx, id, now)
	}); err != nil {
		return err
	}
	if err := db.eachDueTrip(ctx, `
		SELECT id::text FROM trips WHERE status = 'in_progress' AND arrives_at <= $1
		ORDER BY arrives_at LIMIT 100 FOR UPDATE SKIP LOCKED
	`, []any{now.Add(-grace)}, func(tx pgx.Tx, id string) error {
		var approved int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM bookings WHERE trip_id = $1::uuid AND status = 'approved'
		`, id).Scan(&approved); err != nil {
			return dbErr("approved count", err)
		}
		status := ride.TripCompleted
		if approved == 0 {
			status = ride.TripExpired
		}
		return finishTrip(ctx, tx, id, status, now)
	}); err != nil {
		return err
	}
	return db.eachDueTrip(ctx, `
		SELECT t.id::text FROM trips t
		WHERE t.status IN ('active', 'full') AND t.departs_at > $1 AND t.departs_at <= $2
		  AND NOT EXISTS (
			SELECT 1 FROM notifications n
			WHERE n.user_id = t.driver_id AND n.dedupe_key = 'trip_reminder:' || t.id::text)
		ORDER BY t.departs_at LIMIT 100
	`, []any{now, now.Add(remindBefore)}, func(tx pgx.Tx, id string) error {
		key := "trip_reminder:" + id
		var driverID string
		if err := tx.QueryRow(ctx, `SELECT driver_id::text FROM trips WHERE id = $1::uuid`, id).Scan(&driverID); err != nil {
			return dbErr("reminder driver", err)
		}
		if err := insertEvents(ctx, tx, []ride.Event{{UserID: driverID, Type: ride.NoteTripReminder, TripID: id, DedupeKey: key}}); err != nil {
			return err
		}
		return notifyPassengers(ctx, tx, id, ride.NoteTripReminder, "", key, []ride.BookingStatus{ride.BookingApproved})
	})
}

func (db *AppDB) eachDueTrip(ctx context.Context, query string, args []any, fn func(tx pgx.Tx, id string) error) error {
	return db.inTx(ctx, "trip lifecycle", func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return dbErr("due trips", err)
		}
		ids, err := collectIDs(rows)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := fn(tx, id); err != nil {
				return err
			}
		}
		return nil
	})
}
