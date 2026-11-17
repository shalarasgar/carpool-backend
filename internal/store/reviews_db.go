package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// CreateReview stores a rating from one participant of a completed booking about the
// other, and recomputes the target's average from all of their reviews.
func (db *AppDB) CreateReview(ctx context.Context, in ride.ReviewInput) (string, error) {
	var id string
	err := db.inTx(ctx, "create review", func(tx pgx.Tx) error {
		var tripID, passengerID, driverID, status string
		err := tx.QueryRow(ctx, `
			SELECT b.trip_id::text, b.passenger_id::text, t.driver_id::text, b.status::text
			FROM bookings b JOIN trips t ON t.id = b.trip_id WHERE b.id = $1::uuid
		`, in.BookingID).Scan(&tripID, &passengerID, &driverID, &status)
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return ride.ErrNotFound
		}
		if err != nil {
			return dbErr("review booking", err)
		}
		var target string
		switch in.AuthorID {
		case passengerID:
			target = driverID
		case driverID:
			target = passengerID
		default:
			return ride.ErrNotFound
		}
		if ride.BookingStatus(status) != ride.BookingCompleted {
			return ride.ErrReviewNotAllowed
		}
		// A deleted account has no profile to rate.
		var gone bool
		if err := tx.QueryRow(ctx, `
			SELECT deleted_at IS NOT NULL FROM users WHERE id = $1::uuid
		`, target).Scan(&gone); err != nil {
			return dbErr("review target", err)
		}
		if gone {
			return ride.ErrReviewNotAllowed
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO reviews (trip_id, booking_id, from_user_id, to_user_id, rating, comment)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, NULLIF($6, ''))
			ON CONFLICT DO NOTHING
			RETURNING id::text
		`, tripID, in.BookingID, in.AuthorID, target, in.Rating, in.Comment).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ride.ErrReviewExists
		}
		if err != nil {
			return dbErr("insert review", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE users u SET rating_avg = s.avg, rating_count = s.n
			FROM (SELECT ROUND(AVG(rating)::numeric, 2) AS avg, COUNT(*)::int AS n FROM reviews WHERE to_user_id = $1::uuid) s
			WHERE u.id = $1::uuid
		`, target); err != nil {
			return dbErr("update rating", err)
		}
		return insertEvents(ctx, tx, []ride.Event{{
			UserID: target, Type: ride.NoteReviewReceived, TripID: tripID, BookingID: in.BookingID,
			ActorID: in.AuthorID, DedupeKey: "review_received:" + id,
		}})
	})
	return id, err
}

// UserReviews lists the reviews a user received, newest first.
func (db *AppDB) UserReviews(ctx context.Context, userID string, page, limit int) ([]ride.Review, bool, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT r.id::text, r.booking_id::text, r.trip_id::text, r.to_user_id::text, r.rating, COALESCE(r.comment, ''),
		       CASE WHEN t.driver_id = r.from_user_id THEN 'driver' ELSE 'passenger' END, r.created_at,
		       `+summarySelect+`
		FROM reviews r
		JOIN trips t ON t.id = r.trip_id
		JOIN users u ON u.id = r.from_user_id
		WHERE r.to_user_id = $1::uuid
		ORDER BY r.created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit+1, (page-1)*limit)
	if isInvalidUUID(err) {
		return nil, false, ride.ErrNotFound
	}
	if err != nil {
		return nil, false, dbErr("user reviews", err)
	}
	defer rows.Close()
	out := []ride.Review{}
	for rows.Next() {
		var r ride.Review
		dest := []any{&r.ID, &r.BookingID, &r.TripID, &r.ToUserID, &r.Rating, &r.Comment, &r.AuthorRole, &r.CreatedAt}
		dest = append(dest, scanSummary(&r.Author)...)
		if err := rows.Scan(dest...); err != nil {
			return nil, false, dbErr("scan review", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, dbErr("reviews rows", err)
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}
