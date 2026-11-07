package store

import (
	"context"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// SavePushToken binds a device token to the user. A token that was registered by
// another account on the same device moves to this user.
func (db *AppDB) SavePushToken(ctx context.Context, userID, token, platform string, now time.Time) error {
	if _, err := db.pool.Exec(ctx, `
		INSERT INTO push_tokens (token, user_id, platform, updated_at)
		VALUES ($1, $2::uuid, $3, $4)
		ON CONFLICT (token) DO UPDATE
		SET user_id = EXCLUDED.user_id, platform = EXCLUDED.platform, updated_at = EXCLUDED.updated_at
	`, token, userID, platform, now); err != nil {
		return dbErr("save push token", err)
	}
	return nil
}

// DeletePushToken removes the user's token. A token that belongs to someone else is kept.
func (db *AppDB) DeletePushToken(ctx context.Context, userID, token string) error {
	if _, err := db.pool.Exec(ctx, `
		DELETE FROM push_tokens WHERE token = $1 AND user_id = $2::uuid
	`, token, userID); err != nil {
		return dbErr("delete push token", err)
	}
	return nil
}

// DeletePushTokens removes tokens that FCM reported as no longer valid.
func (db *AppDB) DeletePushTokens(ctx context.Context, tokens []string) error {
	if len(tokens) == 0 {
		return nil
	}
	if _, err := db.pool.Exec(ctx, `DELETE FROM push_tokens WHERE token = ANY($1)`, tokens); err != nil {
		return dbErr("delete push tokens", err)
	}
	return nil
}

// PrunePushTokens removes tokens that no app install refreshed since before.
func (db *AppDB) PrunePushTokens(ctx context.Context, before time.Time) error {
	if _, err := db.pool.Exec(ctx, `DELETE FROM push_tokens WHERE updated_at < $1`, before); err != nil {
		return dbErr("prune push tokens", err)
	}
	return nil
}

// ClaimPushes marks up to limit unsent inbox rows as pushed and returns them. Claiming
// before sending gives at-most-once delivery: a push lost to a crash is not repeated,
// and the inbox still shows the notification.
func (db *AppDB) ClaimPushes(ctx context.Context, now time.Time, limit int) ([]ride.PushJob, error) {
	rows, err := db.pool.Query(ctx, `
		WITH claimed AS (
			UPDATE notifications SET pushed_at = $1
			WHERE id IN (
				SELECT id FROM notifications WHERE pushed_at IS NULL
				ORDER BY created_at LIMIT $2 FOR UPDATE SKIP LOCKED)
			RETURNING id, user_id, type, trip_id, booking_id, actor_id, created_at
		)
		SELECT c.id::text, c.type, COALESCE(c.trip_id::text, ''), COALESCE(c.booking_id::text, ''), c.created_at,
		       u.locale,
		       fl.id::text, fl.name_tk, fl.name_ru, fl.name_en,
		       tl.id::text, tl.name_tk, tl.name_ru, tl.name_en,
		       a.id::text, a.first_name, a.last_name,
		       ARRAY(SELECT p.token FROM push_tokens p WHERE p.user_id = c.user_id ORDER BY p.updated_at DESC)
		FROM claimed c
		JOIN users u ON u.id = c.user_id
		LEFT JOIN trips t ON t.id = c.trip_id
		LEFT JOIN locations fl ON fl.id = t.from_location_id
		LEFT JOIN locations tl ON tl.id = t.to_location_id
		LEFT JOIN users a ON a.id = c.actor_id
		ORDER BY c.created_at
	`, now, limit)
	if err != nil {
		return nil, dbErr("claim pushes", err)
	}
	defer rows.Close()
	var out []ride.PushJob
	for rows.Next() {
		var j ride.PushJob
		var typ string
		var flID, flTK, flRU, flEN, tlID, tlTK, tlRU, tlEN, aID, aFirst, aLast *string
		if err := rows.Scan(&j.NotificationID, &typ, &j.TripID, &j.BookingID, &j.CreatedAt, &j.Locale,
			&flID, &flTK, &flRU, &flEN, &tlID, &tlTK, &tlRU, &tlEN, &aID, &aFirst, &aLast, &j.Tokens); err != nil {
			return nil, dbErr("scan push", err)
		}
		j.Type = ride.NotificationType(typ)
		if flID != nil {
			j.From = &ride.Location{ID: *flID, NameTK: deref(flTK), NameRU: deref(flRU), NameEN: deref(flEN)}
		}
		if tlID != nil {
			j.To = &ride.Location{ID: *tlID, NameTK: deref(tlTK), NameRU: deref(tlRU), NameEN: deref(tlEN)}
		}
		if aID != nil {
			j.Actor = &ride.UserSummary{ID: *aID, FirstName: deref(aFirst), LastName: deref(aLast)}
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr("push rows", err)
	}
	return out, nil
}
