package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shalarasgar/carpool-backend/internal/auth"
	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// dbtx is satisfied by both the pool and a transaction.
type dbtx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (db *AppDB) inTx(ctx context.Context, op string, fn func(tx pgx.Tx) error) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return dbErr("begin "+op, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return dbErr("commit "+op, err)
	}
	return nil
}

// summarySelect is the public user columns shared by every embedded user.
const summarySelect = `
	u.id::text, u.first_name, u.last_name, COALESCE(u.photo_url, ''), u.rating_avg::float8, u.rating_count,
	(SELECT COUNT(*) FROM trips ct WHERE ct.driver_id = u.id AND ct.status = 'completed')::int,
	u.deleted_at IS NOT NULL`

func scanSummary(dst *ride.UserSummary) []any {
	return []any{&dst.ID, &dst.FirstName, &dst.LastName, &dst.PhotoURL, &dst.RatingAvg, &dst.RatingCount,
		&dst.TripsAsDriver, &dst.Deleted}
}

// ActiveUser reports whether an access token subject may still use the API. A deleted
// account is rejected like an unknown one, so its access tokens stop working at once.
func (db *AppDB) ActiveUser(ctx context.Context, id string) error {
	var blocked, deleted bool
	err := db.pool.QueryRow(ctx, `
		SELECT is_blocked, deleted_at IS NOT NULL FROM users WHERE id = $1::uuid
	`, id).Scan(&blocked, &deleted)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) || (err == nil && deleted) {
		return auth.ErrUnauthorized
	}
	if err != nil {
		return dbErr("active user", err)
	}
	if blocked {
		return auth.ErrBlocked
	}
	return nil
}

const profileSelect = `
SELECT u.id::text, COALESCE(u.phone, ''), u.first_name, u.last_name, COALESCE(u.bio, ''), COALESCE(u.photo_url, ''),
       u.locale, u.rating_avg::float8, u.rating_count, u.created_at,
       (SELECT COUNT(*) FROM trips t WHERE t.driver_id = u.id AND t.status = 'completed')::int,
       (SELECT COUNT(*) FROM bookings b WHERE b.passenger_id = u.id AND b.status = 'completed')::int
FROM users u`

// liveUser selects one account that is neither blocked nor deleted.
const liveUser = ` WHERE u.id = $1::uuid AND NOT u.is_blocked AND u.deleted_at IS NULL`

func scanProfile(row pgx.Row) (ride.Profile, error) {
	var p ride.Profile
	err := row.Scan(&p.ID, &p.Phone, &p.FirstName, &p.LastName, &p.Bio, &p.PhotoURL,
		&p.Locale, &p.RatingAvg, &p.RatingCount, &p.CreatedAt, &p.TripsAsDriver, &p.TripsAsPassenger)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
		return ride.Profile{}, ride.ErrNotFound
	}
	if err != nil {
		return ride.Profile{}, dbErr("load profile", err)
	}
	return p, nil
}

// Profile loads a user with private fields. Callers must only return it to its owner.
func (db *AppDB) Profile(ctx context.Context, id string) (ride.Profile, error) {
	return scanProfile(db.pool.QueryRow(ctx, profileSelect+liveUser, id))
}

// UpdateProfile applies a validated patch and keeps the legacy name column in sync.
func (db *AppDB) UpdateProfile(ctx context.Context, id string, p ride.ProfilePatch) (ride.Profile, error) {
	tag, err := db.pool.Exec(ctx, `
		UPDATE users SET
			first_name = COALESCE($2, first_name),
			last_name = COALESCE($3, last_name),
			bio = CASE WHEN $4::text IS NULL THEN bio ELSE NULLIF($4, '') END,
			locale = COALESCE($5, locale),
			name = btrim(COALESCE($2, first_name) || ' ' || COALESCE($3, last_name))
		WHERE id = $1::uuid
	`, id, p.FirstName, p.LastName, p.Bio, p.Locale)
	if err != nil {
		return ride.Profile{}, dbErr("update profile", err)
	}
	if tag.RowsAffected() == 0 {
		return ride.Profile{}, ride.ErrNotFound
	}
	return db.Profile(ctx, id)
}

// SetUserPhoto stores the photo URL and returns the previous one so the file can be removed.
func (db *AppDB) SetUserPhoto(ctx context.Context, id, url string) (string, error) {
	var old string
	err := db.pool.QueryRow(ctx, `
		UPDATE users u SET photo_url = NULLIF($2, '')
		FROM (SELECT photo_url FROM users WHERE id = $1::uuid) prev
		WHERE u.id = $1::uuid
		RETURNING COALESCE(prev.photo_url, '')
	`, id, url).Scan(&old)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ride.ErrNotFound
	}
	if err != nil {
		return "", dbErr("set user photo", err)
	}
	return old, nil
}

// PublicProfile loads a user for another viewer. The phone is cleared.
func (db *AppDB) PublicProfile(ctx context.Context, id string) (ride.Profile, error) {
	p, err := scanProfile(db.pool.QueryRow(ctx, profileSelect+liveUser, id))
	if err != nil {
		return ride.Profile{}, err
	}
	p.Phone = ""
	return p, nil
}

// ChangePhone checks the OTP sent to the new number and moves the account to it.
// Every refresh token is kept: the account, not the device, changed its number.
func (db *AppDB) ChangePhone(ctx context.Context, userID string, in auth.VerifySpec) (ride.Profile, error) {
	var invalidCode bool
	err := db.inTx(ctx, "change phone", func(tx pgx.Tx) error {
		matched, err := consumeOTP(ctx, tx, in)
		if err != nil {
			return err
		}
		if !matched {
			invalidCode = true
			return nil
		}
		tag, err := tx.Exec(ctx, `UPDATE users SET phone = $2 WHERE id = $1::uuid`, userID, in.Phone)
		if isUnique(err) {
			return ride.ErrPhoneTaken
		}
		if err != nil {
			return dbErr("change phone", err)
		}
		if tag.RowsAffected() == 0 {
			return ride.ErrNotFound
		}
		return nil
	})
	if err != nil {
		return ride.Profile{}, err
	}
	if invalidCode {
		return ride.Profile{}, auth.ErrInvalidCode
	}
	return db.Profile(ctx, userID)
}

// PhoneInUse reports whether another account already has this number.
func (db *AppDB) PhoneInUse(ctx context.Context, phone, exceptUserID string) (bool, error) {
	var n int
	if err := db.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM users WHERE phone = $1 AND id <> $2::uuid
	`, phone, exceptUserID).Scan(&n); err != nil {
		return false, dbErr("phone in use", err)
	}
	return n > 0, nil
}

// lockProfile locks the user row. Trip creation and schedule edits serialize on it.
func lockProfile(ctx context.Context, tx pgx.Tx, id string) (ride.Profile, error) {
	var p ride.Profile
	err := tx.QueryRow(ctx, `
		SELECT id::text, first_name, last_name FROM users
		WHERE id = $1::uuid AND NOT is_blocked AND deleted_at IS NULL FOR UPDATE
	`, id).Scan(&p.ID, &p.FirstName, &p.LastName)
	if errors.Is(err, pgx.ErrNoRows) {
		return ride.Profile{}, ride.ErrNotFound
	}
	if err != nil {
		return ride.Profile{}, dbErr("lock user", err)
	}
	return p, nil
}

func blockedEitherWay(ctx context.Context, q dbtx, a, b string) (bool, error) {
	var n int
	if err := q.QueryRow(ctx, `
		SELECT COUNT(*) FROM user_blocks
		WHERE (blocker_id = $1::uuid AND blocked_id = $2::uuid) OR (blocker_id = $2::uuid AND blocked_id = $1::uuid)
	`, a, b).Scan(&n); err != nil {
		return false, dbErr("blocked check", err)
	}
	return n > 0, nil
}

func isInvalidUUID(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}

func optTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}
