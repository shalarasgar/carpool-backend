package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// DeleteAccount erases a user's personal data in one transaction and returns the media
// files (profile and vehicle photos) that the caller must remove from storage.
//
// The users row is kept as an empty tombstone because other users' trips, bookings and
// reviews reference it. Everything that identifies the person is removed:
//
//   - upcoming trips the user drives are cancelled and their passengers notified;
//   - the user's pending bookings, and approved ones on trips that have not departed, are
//     cancelled and the drivers notified;
//   - vehicles, sessions, push tokens, the inbox, blocks, OTP rows, finished SMS rows and
//     the reviews about the user are deleted;
//   - free text the user wrote (trip notes, meeting points, booking messages, review
//     comments) is cleared; the ratings the user gave to others stay, without the comment;
//   - name, photo, bio and rating are cleared and the phone number is released.
//
// A trip the user drives that has already departed cannot be cancelled, so the deletion is
// refused with ErrTripInProgress until the driver completes it. An account blocked by an
// admin keeps its phone number, so deleting the account does not lift the ban.
//
// Lock order: the users row first, then trips in id order, then bookings. This matches
// trip publication (users, then trips) and booking changes (trip, then booking).
func (db *AppDB) DeleteAccount(ctx context.Context, userID string, now time.Time) ([]string, error) {
	var files []string
	err := db.inTx(ctx, "delete account", func(tx pgx.Tx) error {
		var phone, photo *string
		err := tx.QueryRow(ctx, `
			SELECT phone, photo_url FROM users WHERE id = $1::uuid AND deleted_at IS NULL FOR UPDATE
		`, userID).Scan(&phone, &photo)
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return ride.ErrNotFound
		}
		if err != nil {
			return dbErr("lock account", err)
		}
		if err := closeOpenRides(ctx, tx, userID, now); err != nil {
			return err
		}
		vehiclePhotos, err := deleteVehicles(ctx, tx, userID)
		if err != nil {
			return err
		}
		files = append(vehiclePhotos, deref(photo))
		return eraseUserData(ctx, tx, userID, deref(phone), now)
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// closeOpenRides cancels what the user still takes part in: live trips as the driver and
// open bookings as a passenger.
func closeOpenRides(ctx context.Context, tx pgx.Tx, userID string, now time.Time) error {
	rows, err := tx.Query(ctx, `
		SELECT id::text FROM trips
		WHERE driver_id = $1::uuid AND status IN ('active', 'full', 'in_progress')
		UNION
		SELECT trip_id::text FROM bookings
		WHERE passenger_id = $1::uuid AND status IN ('pending', 'approved')
		ORDER BY 1
	`, userID)
	if err != nil {
		return dbErr("open rides", err)
	}
	tripIDs, err := collectIDs(rows)
	if err != nil {
		return err
	}
	for _, id := range tripIDs {
		t, err := lockTrip(ctx, tx, id)
		if err != nil {
			return err
		}
		if t.DriverID == userID {
			err = closeDrivenTrip(ctx, tx, t, now)
		} else {
			err = closeOwnBooking(ctx, tx, t, userID, now)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func closeDrivenTrip(ctx context.Context, tx pgx.Tx, t lockedTrip, now time.Time) error {
	if !t.Status.Live() {
		return nil
	}
	if err := cancelLockedTrip(ctx, tx, t, now); err != nil {
		if errors.Is(err, ride.ErrTripStarted) {
			return ride.ErrTripInProgress
		}
		return err
	}
	return nil
}

// closeOwnBooking cancels the user's open booking on t. An approved booking on a trip that
// has departed is left alone: it completes with the trip, under the erased account.
func closeOwnBooking(ctx context.Context, tx pgx.Tx, t lockedTrip, userID string, now time.Time) error {
	var b lockedBooking
	var status string
	err := tx.QueryRow(ctx, `
		SELECT id::text, trip_id::text, passenger_id::text, status::text, seats, pickup_order, dropoff_order
		FROM bookings
		WHERE trip_id = $1::uuid AND passenger_id = $2::uuid AND status IN ('pending', 'approved')
		FOR UPDATE
	`, t.ID, userID).Scan(&b.ID, &b.TripID, &b.PassengerID, &status, &b.Seats, &b.PickupOrder, &b.DropoffOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return dbErr("lock own booking", err)
	}
	b.Status = ride.BookingStatus(status)
	if b.Status == ride.BookingApproved && !t.DepartsAt.After(now) {
		return nil
	}
	return cancelLockedBooking(ctx, tx, t, b, userID, now)
}

// deleteVehicles removes the user's vehicles and returns their photo files. Past trips
// keep running without a vehicle reference (trips.vehicle_id is ON DELETE SET NULL).
func deleteVehicles(ctx context.Context, tx pgx.Tx, userID string) ([]string, error) {
	rows, err := tx.Query(ctx, `
		DELETE FROM vehicles WHERE user_id = $1::uuid RETURNING COALESCE(photo_url, '')
	`, userID)
	if err != nil {
		return nil, dbErr("delete vehicles", err)
	}
	return collectIDs(rows)
}

// eraseUserData removes or clears every row that identifies the user, then empties the
// users row itself.
func eraseUserData(ctx context.Context, tx pgx.Tx, userID, phone string, now time.Time) error {
	steps := []struct {
		op  string
		sql string
	}{
		{"clear trip texts", `UPDATE trips SET note = NULL, conditions = NULL WHERE driver_id = $1::uuid`},
		{"clear meeting points", `
			UPDATE trip_stops SET meeting_point = NULL
			WHERE trip_id IN (SELECT id FROM trips WHERE driver_id = $1::uuid)`},
		{"clear booking messages", `UPDATE bookings SET message = NULL WHERE passenger_id = $1::uuid`},
		{"delete received reviews", `DELETE FROM reviews WHERE to_user_id = $1::uuid`},
		{"clear written reviews", `UPDATE reviews SET comment = NULL WHERE from_user_id = $1::uuid`},
		{"delete inbox", `DELETE FROM notifications WHERE user_id = $1::uuid`},
		{"delete push tokens", `DELETE FROM push_tokens WHERE user_id = $1::uuid`},
		{"delete device tokens", `DELETE FROM device_tokens WHERE user_id = $1::uuid`},
		{"delete sessions", `DELETE FROM refresh_tokens WHERE user_id = $1::uuid`},
		{"delete blocks", `DELETE FROM user_blocks WHERE blocker_id = $1::uuid OR blocked_id = $1::uuid`},
		// Reports about an account that no longer exists are moot. Reports the user filed
		// about others stay for moderation; they now point at the empty row.
		{"delete reports about user", `
			DELETE FROM reports r USING users u
			WHERE r.reported_user_id = $1::uuid AND u.id = r.reported_user_id AND NOT u.is_blocked`},
	}
	for _, s := range steps {
		if _, err := tx.Exec(ctx, s.sql, userID); err != nil {
			return dbErr(s.op, err)
		}
	}
	if phone != "" {
		if _, err := tx.Exec(ctx, `DELETE FROM otp_codes WHERE phone = $1`, phone); err != nil {
			return dbErr("delete otp codes", err)
		}
		// A message a gateway is sending right now is left to the queue's own cleanup.
		if _, err := tx.Exec(ctx, `
			DELETE FROM sms_queue WHERE phone = $1 AND status IN ('sent', 'failed')
		`, phone); err != nil {
			return dbErr("delete sms rows", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users SET
			phone = CASE WHEN is_blocked THEN phone END,
			name = '', first_name = '', last_name = '', bio = NULL, photo_url = NULL,
			rating_avg = 0, rating_count = 0, deleted_at = $2
		WHERE id = $1::uuid
	`, userID, now); err != nil {
		return dbErr("erase user", err)
	}
	return nil
}

// UserIDByPhone finds the live account that owns a phone number.
func (db *AppDB) UserIDByPhone(ctx context.Context, phone string) (string, error) {
	var id string
	err := db.pool.QueryRow(ctx, `
		SELECT id::text FROM users WHERE phone = $1 AND deleted_at IS NULL
	`, phone).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ride.ErrNotFound
	}
	if err != nil {
		return "", dbErr("user by phone", err)
	}
	return id, nil
}
