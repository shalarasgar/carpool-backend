package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// Overview counts the admin dashboard figures in one round trip. since is the start of the
// local day; "new" and "failed" counts cover rows created from then on.
func (db *AppDB) Overview(ctx context.Context, since time.Time) (ride.Overview, error) {
	o := ride.Overview{Since: since}
	err := db.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM reports WHERE status = 'open')::int,
			(SELECT COUNT(*) FROM users WHERE deleted_at IS NULL)::int,
			(SELECT COUNT(*) FROM users WHERE created_at >= $1)::int,
			(SELECT COUNT(*) FROM users WHERE is_blocked)::int,
			(SELECT COUNT(*) FROM trips WHERE status IN ('active', 'full', 'in_progress'))::int,
			(SELECT COUNT(*) FROM trips WHERE created_at >= $1)::int,
			(SELECT COUNT(*) FROM bookings WHERE status = 'pending')::int,
			(SELECT COUNT(*) FROM bookings WHERE created_at >= $1)::int,
			(SELECT COUNT(*) FROM sms_queue WHERE status = 'pending')::int,
			(SELECT COUNT(*) FROM sms_queue WHERE status = 'failed' AND created_at >= $1)::int,
			(SELECT COUNT(*) FROM sms_gateways WHERE is_active)::int,
			(SELECT MAX(last_heartbeat_at) FROM sms_gateways WHERE is_active)
	`, since).Scan(&o.ReportsOpen, &o.UsersTotal, &o.UsersNew, &o.UsersBlocked, &o.TripsUpcoming, &o.TripsNew,
		&o.BookingsPending, &o.BookingsNew, &o.SMSPending, &o.SMSFailed, &o.GatewaysActive, &o.LastHeartbeat)
	if err != nil {
		return ride.Overview{}, dbErr("overview", err)
	}
	return o, nil
}

const adminUserSelect = `
	u.id::text, COALESCE(u.phone, ''), u.first_name, u.last_name, COALESCE(u.photo_url, ''),
	u.rating_avg::float8, u.rating_count, u.is_blocked, u.deleted_at IS NOT NULL, u.created_at`

func scanAdminUser(dst *ride.AdminUser) []any {
	return []any{&dst.ID, &dst.Phone, &dst.FirstName, &dst.LastName, &dst.PhotoURL,
		&dst.RatingAvg, &dst.RatingCount, &dst.Blocked, &dst.Deleted, &dst.CreatedAt}
}

// likeEscaper escapes the LIKE wildcards in user input, so "50%" is searched literally.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// AdminUsers searches accounts for a moderator, newest first. q must be normalized: a
// name matches when the query has letters, a phone when it has three or more digits.
func (db *AppDB) AdminUsers(ctx context.Context, q ride.UserQuery, page, limit int) ([]ride.AdminUser, bool, error) {
	var phonePattern, namePattern string
	if q.Digits != "" {
		phonePattern = "%" + q.Digits + "%"
	}
	if q.HasLetters() {
		namePattern = "%" + likeEscaper.Replace(q.Text) + "%"
	}
	rows, err := db.pool.Query(ctx, `
		SELECT `+adminUserSelect+`,
		       (SELECT COUNT(*) FROM reports r WHERE r.reported_user_id = u.id AND r.status = 'open')::int
		FROM users u
		WHERE CASE $1
		          WHEN 'active' THEN u.deleted_at IS NULL AND NOT u.is_blocked
		          WHEN 'blocked' THEN u.is_blocked
		          WHEN 'deleted' THEN u.deleted_at IS NOT NULL
		          ELSE u.deleted_at IS NULL
		      END
		  AND ($2 = ''
		       OR ($3 <> '' AND u.phone LIKE $3)
		       OR ($4 <> '' AND btrim(u.first_name || ' ' || u.last_name) ILIKE $4))
		ORDER BY u.created_at DESC, u.id
		LIMIT $5 OFFSET $6
	`, string(q.State), q.Text, phonePattern, namePattern, limit+1, (page-1)*limit)
	if err != nil {
		return nil, false, dbErr("admin users", err)
	}
	defer rows.Close()
	out := []ride.AdminUser{}
	for rows.Next() {
		var u ride.AdminUser
		if err := rows.Scan(append(scanAdminUser(&u), &u.OpenReports)...); err != nil {
			return nil, false, dbErr("scan admin user", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, false, dbErr("admin users rows", err)
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}

// AdminUser loads the moderator's card of any account, deleted or banned included.
func (db *AppDB) AdminUser(ctx context.Context, id string) (ride.AdminUserDetail, error) {
	var d ride.AdminUserDetail
	a := &d.Activity
	dest := append(scanAdminUser(&d.AdminUser), &d.Bio, &d.Locale, &d.DeletedAt,
		&a.TripsUpcoming, &a.TripsCompleted, &a.TripsCancelled, &a.DriverCancellations,
		&a.BookingsActive, &a.BookingsCompleted, &a.PassengerCancellations,
		&a.ReportsAgainst, &a.ReportsAgainstOpen, &a.Reporters, &a.ReportsFiled, &a.BlockedBy)
	err := db.pool.QueryRow(ctx, `
		SELECT `+adminUserSelect+`, COALESCE(u.bio, ''), u.locale, u.deleted_at,
		       (SELECT COUNT(*) FROM trips t WHERE t.driver_id = u.id AND t.status IN ('active', 'full', 'in_progress'))::int,
		       (SELECT COUNT(*) FROM trips t WHERE t.driver_id = u.id AND t.status = 'completed')::int,
		       (SELECT COUNT(*) FROM trips t WHERE t.driver_id = u.id AND t.status = 'cancelled')::int,
		       (SELECT COUNT(*) FROM bookings b JOIN trips t ON t.id = b.trip_id
		         WHERE t.driver_id = u.id AND b.status = 'cancelled_by_driver')::int,
		       (SELECT COUNT(*) FROM bookings b WHERE b.passenger_id = u.id AND b.status IN ('pending', 'approved'))::int,
		       (SELECT COUNT(*) FROM bookings b WHERE b.passenger_id = u.id AND b.status = 'completed')::int,
		       (SELECT COUNT(*) FROM bookings b WHERE b.passenger_id = u.id AND b.status = 'cancelled_by_passenger')::int,
		       h.total, h.open, h.reporters,
		       (SELECT COUNT(*) FROM reports x WHERE x.reporter_id = u.id)::int,
		       (SELECT COUNT(*) FROM user_blocks ub WHERE ub.blocked_id = u.id)::int
		FROM users u
		CROSS JOIN LATERAL (
			SELECT COUNT(*)::int AS total,
			       (COUNT(*) FILTER (WHERE x.status = 'open'))::int AS open,
			       COUNT(DISTINCT x.reporter_id)::int AS reporters
			FROM reports x WHERE x.reported_user_id = u.id
		) h
		WHERE u.id = $1::uuid
	`, id).Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
		return ride.AdminUserDetail{}, ride.ErrNotFound
	}
	if err != nil {
		return ride.AdminUserDetail{}, dbErr("admin user", err)
	}
	d.OpenReports = a.ReportsAgainstOpen
	if d.Vehicles, err = db.ListVehicles(ctx, id); err != nil {
		return ride.AdminUserDetail{}, err
	}
	return d, nil
}
