package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// maxBlockedList bounds the list of users one person blocked.
const maxBlockedList = 200

// userExists reports whether id is an account that has not been deleted.
func userExists(ctx context.Context, q dbtx, id string) (bool, error) {
	var found bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM users WHERE id = $1::uuid AND deleted_at IS NULL)
	`, id).Scan(&found)
	if isInvalidUUID(err) {
		return false, nil
	}
	if err != nil {
		return false, dbErr("user exists", err)
	}
	return found, nil
}

// BlockUser hides two users from each other: neither sees the other's trips in search and
// neither can book the other's trips. Blocking the same user again changes nothing.
func (db *AppDB) BlockUser(ctx context.Context, blockerID, blockedID string) error {
	found, err := userExists(ctx, db.pool, blockedID)
	if err != nil {
		return err
	}
	if !found {
		return ride.ErrNotFound
	}
	if _, err := db.pool.Exec(ctx, `
		INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1::uuid, $2::uuid)
		ON CONFLICT DO NOTHING
	`, blockerID, blockedID); err != nil {
		return dbErr("block user", err)
	}
	return nil
}

// UnblockUser removes a block. Removing a block that does not exist is not an error.
func (db *AppDB) UnblockUser(ctx context.Context, blockerID, blockedID string) error {
	_, err := db.pool.Exec(ctx, `
		DELETE FROM user_blocks WHERE blocker_id = $1::uuid AND blocked_id = $2::uuid
	`, blockerID, blockedID)
	if isInvalidUUID(err) {
		return nil
	}
	if err != nil {
		return dbErr("unblock user", err)
	}
	return nil
}

// BlockedUsers lists the users blockerID blocked, newest first.
func (db *AppDB) BlockedUsers(ctx context.Context, blockerID string) ([]ride.UserSummary, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT `+summarySelect+`
		FROM user_blocks ub
		JOIN users u ON u.id = ub.blocked_id
		WHERE ub.blocker_id = $1::uuid
		ORDER BY ub.created_at DESC
		LIMIT $2
	`, blockerID, maxBlockedList)
	if err != nil {
		return nil, dbErr("blocked users", err)
	}
	defer rows.Close()
	out := []ride.UserSummary{}
	for rows.Next() {
		var u ride.UserSummary
		if err := rows.Scan(scanSummary(&u)...); err != nil {
			return nil, dbErr("scan blocked user", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr("blocked users rows", err)
	}
	return out, nil
}

// CreateReport stores a report for the moderators. The reported user must exist; a trip,
// when given, must exist too.
func (db *AppDB) CreateReport(ctx context.Context, in ride.ReportInput) (string, error) {
	found, err := userExists(ctx, db.pool, in.ReportedUserID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", ride.ErrNotFound
	}
	if in.TripID != "" {
		var tripFound bool
		if err := db.pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM trips WHERE id = $1::uuid)
		`, in.TripID).Scan(&tripFound); err != nil {
			return "", dbErr("report trip", err)
		}
		if !tripFound {
			return "", &ride.FieldError{Field: "trip_id", Reason: "not_found"}
		}
	}
	var id string
	if err := db.pool.QueryRow(ctx, `
		INSERT INTO reports (reporter_id, reported_user_id, trip_id, reason, details)
		VALUES ($1::uuid, $2::uuid, NULLIF($3, '')::uuid, $4, NULLIF($5, ''))
		RETURNING id::text
	`, in.ReporterID, in.ReportedUserID, in.TripID, string(in.Reason), in.Details).Scan(&id); err != nil {
		return "", dbErr("insert report", err)
	}
	return id, nil
}

// Reports lists reports for a moderator, oldest first so the queue is worked in order.
// An empty status lists every report.
func (db *AppDB) Reports(ctx context.Context, status ride.ReportStatus, page, limit int) ([]ride.Report, bool, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT r.id::text, r.reporter_id::text, btrim(a.first_name || ' ' || a.last_name),
		       r.reported_user_id::text, btrim(b.first_name || ' ' || b.last_name),
		       b.is_blocked, b.deleted_at IS NOT NULL,
		       COALESCE(r.trip_id::text, ''), r.reason, COALESCE(r.details, ''), r.status, r.created_at, r.resolved_at
		FROM reports r
		JOIN users a ON a.id = r.reporter_id
		JOIN users b ON b.id = r.reported_user_id
		WHERE $1 = '' OR r.status = $1
		ORDER BY r.created_at, r.id
		LIMIT $2 OFFSET $3
	`, string(status), limit+1, (page-1)*limit)
	if err != nil {
		return nil, false, dbErr("reports", err)
	}
	defer rows.Close()
	out := []ride.Report{}
	for rows.Next() {
		var r ride.Report
		var reason, st string
		if err := rows.Scan(&r.ID, &r.ReporterID, &r.ReporterName, &r.ReportedUserID, &r.ReportedName,
			&r.ReportedBlocked, &r.ReportedDeleted, &r.TripID, &reason, &r.Details, &st, &r.CreatedAt, &r.ResolvedAt); err != nil {
			return nil, false, dbErr("scan report", err)
		}
		r.Reason, r.Status = ride.ReportReason(reason), ride.ReportStatus(st)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, dbErr("reports rows", err)
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}

// ResolveReport closes a report. Closing a closed report keeps its first resolution time.
func (db *AppDB) ResolveReport(ctx context.Context, id string, now time.Time) error {
	tag, err := db.pool.Exec(ctx, `
		UPDATE reports SET status = 'resolved', resolved_at = COALESCE(resolved_at, $2) WHERE id = $1::uuid
	`, id, now)
	if isInvalidUUID(err) {
		return ride.ErrNotFound
	}
	if err != nil {
		return dbErr("resolve report", err)
	}
	if tag.RowsAffected() == 0 {
		return ride.ErrNotFound
	}
	return nil
}

// SetUserBlocked bans or reinstates an account. A banned user cannot sign in and their
// access tokens stop working on the next request. Banning also ends their sessions and
// removes their push tokens.
//
// A deleted account that was banned kept its phone number to hold the ban; lifting the ban
// releases the number, so the row is a plain deleted account again.
func (db *AppDB) SetUserBlocked(ctx context.Context, userID string, blocked bool) error {
	return db.inTx(ctx, "set user blocked", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE users SET
				is_blocked = $2,
				phone = CASE WHEN deleted_at IS NOT NULL AND NOT $2 THEN NULL ELSE phone END
			WHERE id = $1::uuid
		`, userID, blocked)
		if isInvalidUUID(err) {
			return ride.ErrNotFound
		}
		if err != nil {
			return dbErr("set user blocked", err)
		}
		if tag.RowsAffected() == 0 {
			return ride.ErrNotFound
		}
		if !blocked {
			return nil
		}
		if _, err := tx.Exec(ctx, `DELETE FROM refresh_tokens WHERE user_id = $1::uuid`, userID); err != nil {
			return dbErr("end sessions", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM push_tokens WHERE user_id = $1::uuid`, userID); err != nil {
			return dbErr("drop push tokens", err)
		}
		return nil
	})
}
