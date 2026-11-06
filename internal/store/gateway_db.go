package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shalarasgar/carpool-backend/internal/auth"
	"github.com/shalarasgar/carpool-backend/internal/smsqueue"
)

// AppDB is the PostgreSQL repository for OTP challenges and the SMS gateway queue.
type AppDB struct {
	pool *pgxpool.Pool
}

// NewAppDB binds gateway and auth queries to the pool.
func NewAppDB(pool *pgxpool.Pool) *AppDB {
	return &AppDB{pool: pool}
}

// CreateChallenge stores a hashed OTP and a pending SMS in one transaction.
func (db *AppDB) CreateChallenge(ctx context.Context, in auth.Challenge) (auth.ChallengeResult, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return auth.ChallengeResult{}, dbErr("begin challenge", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`, in.Phone); err != nil {
		return auth.ChallengeResult{}, dbErr("lock phone", err)
	}
	if !in.SkipLimits {
		if err := enforceOTPLimits(ctx, tx, in); err != nil {
			return auth.ChallengeResult{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE otp_codes SET used = true WHERE phone = $1 AND used = false`, in.Phone); err != nil {
		return auth.ChallengeResult{}, dbErr("retire otp", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO otp_codes (phone, code_hash, expires_at, ip, device_id)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''))
	`, in.Phone, in.CodeHash, in.ExpiresAt, nullString(in.IP), in.DeviceID); err != nil {
		return auth.ChallengeResult{}, dbErr("insert otp", err)
	}

	gateways, err := listGateways(ctx, tx)
	if err != nil {
		return auth.ChallengeResult{}, err
	}
	gatewayID, ok := smsqueue.ChooseGateway(in.Phone, gateways, in.Now)
	if _, err := tx.Exec(ctx, `
		INSERT INTO sms_queue (phone, message, gateway_id)
		VALUES ($1, $2, NULLIF($3, '')::uuid)
	`, in.Phone, in.Message, gatewayID); err != nil {
		return auth.ChallengeResult{}, dbErr("insert sms", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return auth.ChallengeResult{}, dbErr("commit challenge", err)
	}
	return auth.ChallengeResult{NoHealthyGateway: !ok}, nil
}

func enforceOTPLimits(ctx context.Context, tx pgx.Tx, in auth.Challenge) error {
	var sinceLast *time.Time
	err := tx.QueryRow(ctx, `
		SELECT created_at FROM otp_codes WHERE phone = $1 ORDER BY created_at DESC LIMIT 1
	`, in.Phone).Scan(&sinceLast)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return dbErr("latest otp", err)
	}
	if sinceLast != nil && in.Now.Sub(*sinceLast) < in.PhoneInterval {
		return &smsqueue.RateLimited{RetryAfter: in.PhoneInterval - in.Now.Sub(*sinceLast)}
	}

	var daily int
	var oldestDaily *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*), MIN(created_at)
		FROM otp_codes
		WHERE phone = $1 AND created_at > $2
	`, in.Phone, in.Now.Add(-24*time.Hour)).Scan(&daily, &oldestDaily); err != nil {
		return dbErr("daily otp", err)
	}
	if daily >= in.PhoneDailyMax && oldestDaily != nil {
		return &smsqueue.RateLimited{RetryAfter: oldestDaily.Add(24 * time.Hour).Sub(in.Now)}
	}

	if in.IP != "" {
		var ipCount int
		var oldestIP *time.Time
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*), MIN(created_at) FROM otp_codes
			WHERE ip = $1 AND created_at > $2
		`, in.IP, in.Now.Add(-time.Hour)).Scan(&ipCount, &oldestIP); err != nil {
			return dbErr("ip otp", err)
		}
		if ipCount >= in.IPHourlyMax && oldestIP != nil {
			return &smsqueue.RateLimited{RetryAfter: oldestIP.Add(time.Hour).Sub(in.Now)}
		}
	}
	if in.DeviceID != "" {
		var deviceCount int
		var oldestDevice *time.Time
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*), MIN(created_at) FROM otp_codes
			WHERE device_id = $1 AND created_at > $2
		`, in.DeviceID, in.Now.Add(-time.Hour)).Scan(&deviceCount, &oldestDevice); err != nil {
			return dbErr("device otp", err)
		}
		if deviceCount >= in.DeviceHourlyMax && oldestDevice != nil {
			return &smsqueue.RateLimited{RetryAfter: oldestDevice.Add(time.Hour).Sub(in.Now)}
		}
	}
	return nil
}

// VerifyOTP checks the latest unused code, then issues a refresh token.
func (db *AppDB) VerifyOTP(ctx context.Context, in auth.VerifySpec) (auth.SessionUser, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return auth.SessionUser{}, dbErr("begin verify", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	matched, err := consumeOTP(ctx, tx, in)
	if err != nil {
		return auth.SessionUser{}, err
	}
	if !matched {
		if err := tx.Commit(ctx); err != nil {
			return auth.SessionUser{}, dbErr("commit otp attempt", err)
		}
		return auth.SessionUser{}, auth.ErrInvalidCode
	}

	user, err := upsertUser(ctx, tx, in.Phone, in.Locale)
	if errors.Is(err, auth.ErrBlocked) {
		if cerr := tx.Commit(ctx); cerr != nil {
			return auth.SessionUser{}, dbErr("commit blocked verify", cerr)
		}
		return auth.SessionUser{}, auth.ErrBlocked
	}
	if err != nil {
		return auth.SessionUser{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, token_hash, device_id, expires_at)
		VALUES ($1::uuid, $2, $3, $4)
	`, user.ID, in.RefreshHash, in.DeviceID, in.RefreshExpiry); err != nil {
		return auth.SessionUser{}, dbErr("insert refresh", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return auth.SessionUser{}, dbErr("commit verify", err)
	}
	return user, nil
}

// consumeOTP locks the phone and checks its latest unused code. It returns true and marks
// the code used on a match. On a miss it records the attempt (or expiry) and returns false;
// the caller must commit so the attempt counter is kept, then report auth.ErrInvalidCode.
func consumeOTP(ctx context.Context, tx pgx.Tx, in auth.VerifySpec) (bool, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`, in.Phone); err != nil {
		return false, dbErr("lock phone", err)
	}

	var (
		otpID    string
		hash     string
		expires  time.Time
		attempts int
	)
	err := tx.QueryRow(ctx, `
		SELECT id::text, code_hash, expires_at, attempts
		FROM otp_codes
		WHERE phone = $1 AND used = false
		ORDER BY created_at DESC
		LIMIT 1
		FOR UPDATE
	`, in.Phone).Scan(&otpID, &hash, &expires, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, dbErr("load otp", err)
	}
	if !expires.After(in.Now) {
		if _, err := tx.Exec(ctx, `UPDATE otp_codes SET used = true WHERE id = $1::uuid`, otpID); err != nil {
			return false, dbErr("expire otp", err)
		}
		return false, nil
	}
	if in.Match == nil || !in.Match(hash) {
		attempts++
		used := attempts >= in.MaxAttempts
		if _, err := tx.Exec(ctx, `UPDATE otp_codes SET attempts = $2, used = $3 WHERE id = $1::uuid`, otpID, attempts, used); err != nil {
			return false, dbErr("bump otp", err)
		}
		return false, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE otp_codes SET used = true WHERE id = $1::uuid`, otpID); err != nil {
		return false, dbErr("use otp", err)
	}
	return true, nil
}

func upsertUser(ctx context.Context, tx pgx.Tx, phone, locale string) (auth.SessionUser, error) {
	var user auth.SessionUser
	var blocked bool
	err := tx.QueryRow(ctx, `
		SELECT id::text, phone, name, locale, is_blocked
		FROM users WHERE phone = $1 FOR UPDATE
	`, phone).Scan(&user.ID, &user.Phone, &user.Name, &user.Locale, &blocked)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			INSERT INTO users (phone, locale) VALUES ($1, $2)
			RETURNING id::text, phone, name, locale
		`, phone, locale).Scan(&user.ID, &user.Phone, &user.Name, &user.Locale)
		if err != nil {
			return auth.SessionUser{}, dbErr("insert user", err)
		}
		user.IsNew = true
		return user, nil
	}
	if err != nil {
		return auth.SessionUser{}, dbErr("load user", err)
	}
	if blocked {
		return auth.SessionUser{}, auth.ErrBlocked
	}
	return user, nil
}

// RotateRefresh revokes the presented token and stores its replacement.
// A revoked token revokes every active token for that user.
func (db *AppDB) RotateRefresh(ctx context.Context, oldHash, newHash, deviceID string, exp time.Time) (auth.SessionUser, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return auth.SessionUser{}, dbErr("begin refresh", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID string
	var revoked *time.Time
	var storedExp time.Time
	var storedDevice string
	err = tx.QueryRow(ctx, `
		SELECT user_id::text, revoked_at, expires_at, device_id
		FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE
	`, oldHash).Scan(&userID, &revoked, &storedExp, &storedDevice)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.SessionUser{}, auth.ErrUnauthorized
	}
	if err != nil {
		return auth.SessionUser{}, dbErr("load refresh", err)
	}
	if revoked != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE refresh_tokens SET revoked_at = now()
			WHERE user_id = $1::uuid AND revoked_at IS NULL
		`, userID); err != nil {
			return auth.SessionUser{}, dbErr("revoke family", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return auth.SessionUser{}, dbErr("commit reuse", err)
		}
		return auth.SessionUser{}, auth.ErrUnauthorized
	}
	if !storedExp.After(time.Now()) {
		_, _ = tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE token_hash = $1`, oldHash)
		_ = tx.Commit(ctx)
		return auth.SessionUser{}, auth.ErrUnauthorized
	}
	var user auth.SessionUser
	var blocked bool
	if err := tx.QueryRow(ctx, `
		SELECT id::text, phone, name, locale, is_blocked FROM users WHERE id = $1::uuid
	`, userID).Scan(&user.ID, &user.Phone, &user.Name, &user.Locale, &blocked); err != nil {
		return auth.SessionUser{}, dbErr("load refresh user", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE token_hash = $1`, oldHash); err != nil {
		return auth.SessionUser{}, dbErr("revoke refresh", err)
	}
	if blocked {
		if err := tx.Commit(ctx); err != nil {
			return auth.SessionUser{}, dbErr("commit blocked refresh", err)
		}
		return auth.SessionUser{}, auth.ErrBlocked
	}
	device := storedDevice
	if deviceID != "" {
		device = deviceID
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, token_hash, device_id, expires_at)
		VALUES ($1::uuid, $2, $3, $4)
	`, userID, newHash, device, exp); err != nil {
		return auth.SessionUser{}, dbErr("insert rotated refresh", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return auth.SessionUser{}, dbErr("commit refresh", err)
	}
	return user, nil
}

// RevokeRefresh marks a refresh token revoked. An unknown token is not an error.
func (db *AppDB) RevokeRefresh(ctx context.Context, hash string) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = COALESCE(revoked_at, now()) WHERE token_hash = $1
	`, hash)
	if err != nil {
		return dbErr("revoke refresh", err)
	}
	return nil
}

// GatewayByHash loads an active gateway credential.
func (db *AppDB) GatewayByHash(ctx context.Context, hash string) (smsqueue.Gateway, error) {
	g, err := scanGateway(db.pool.QueryRow(ctx, gatewaySelect+` WHERE api_key_hash = $1`, hash))
	if errors.Is(err, pgx.ErrNoRows) {
		return smsqueue.Gateway{}, smsqueue.ErrUnauthorized
	}
	if err != nil {
		return smsqueue.Gateway{}, dbErr("load gateway", err)
	}
	if !g.Active {
		return smsqueue.Gateway{}, smsqueue.ErrUnauthorized
	}
	return g, nil
}

// TouchHeartbeat records that the gateway is online.
func (db *AppDB) TouchHeartbeat(ctx context.Context, id string, now time.Time) error {
	tag, err := db.pool.Exec(ctx, `
		UPDATE sms_gateways SET last_heartbeat_at = $2 WHERE id = $1::uuid AND is_active
	`, id, now)
	if err != nil {
		return dbErr("heartbeat", err)
	}
	if tag.RowsAffected() == 0 {
		return smsqueue.ErrUnauthorized
	}
	return nil
}

// ClaimPending locks up to limit eligible jobs for this gateway.
// Eligibility is smsqueue.Eligible, so the SQL and the tests share one rule.
func (db *AppDB) ClaimPending(ctx context.Context, claimer smsqueue.Gateway, limit int, lease time.Duration, now time.Time) ([]smsqueue.Job, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return nil, dbErr("begin claim", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	healthy, err := healthyIDs(ctx, tx, now.Add(-smsqueue.HealthyWindow))
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, phone, message, status, attempts, gateway_id::text, last_error,
		       locked_until, send_started_at, telephony_accepted_at, delivery_status, sent_at, created_at
		FROM sms_queue
		WHERE status = 'pending'
		ORDER BY created_at
		LIMIT 50
		FOR UPDATE SKIP LOCKED
	`)
	if err != nil {
		return nil, dbErr("select pending", err)
	}
	pending, err := collectJobs(rows)
	if err != nil {
		return nil, err
	}

	var claimed []smsqueue.Job
	until := now.Add(lease)
	for _, job := range pending {
		if len(claimed) >= limit {
			break
		}
		if !smsqueue.Eligible(job, claimer, healthy) {
			continue
		}
		job.Status = smsqueue.StatusProcessing
		job.GatewayID = claimer.ID
		job.LockedUntil = until
		job.SendStartedAt = time.Time{}
		job.TelephonyAcceptedAt = time.Time{}
		job.DeliveryStatus = ""
		job.LastError = ""
		if err := saveJob(ctx, tx, job); err != nil {
			return nil, err
		}
		claimed = append(claimed, job)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE sms_gateways SET last_heartbeat_at = $2 WHERE id = $1::uuid AND is_active
	`, claimer.ID, now); err != nil {
		return nil, dbErr("claim heartbeat", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, dbErr("commit claim", err)
	}
	return claimed, nil
}

// MutateJob locks one job, applies fn, and writes the result.
func (db *AppDB) MutateJob(ctx context.Context, id string, fn func(*smsqueue.Job) error) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return dbErr("begin mutate", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, jobSelect+` WHERE id = $1::uuid FOR UPDATE`, id)
	if err != nil {
		return dbErr("lock job", err)
	}
	jobs, err := collectJobs(rows)
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		return smsqueue.ErrNotFound
	}
	job := jobs[0]
	if err := fn(&job); err != nil {
		return err
	}
	if err := saveJob(ctx, tx, job); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return dbErr("commit job", err)
	}
	return nil
}

// ExpireDue applies smsqueue.Expire to a bounded batch of due jobs.
func (db *AppDB) ExpireDue(ctx context.Context, maxAge time.Duration, now time.Time) (int, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return 0, dbErr("begin expire", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, jobSelect+`
		WHERE (
			send_started_at IS NULL
			AND status IN ('pending', 'processing')
			AND created_at < $1
		) OR (
			status = 'processing' AND locked_until IS NOT NULL AND locked_until < $2
		)
		ORDER BY created_at
		LIMIT 100
		FOR UPDATE SKIP LOCKED
	`, now.Add(-maxAge), now)
	if err != nil {
		return 0, dbErr("select expired", err)
	}
	jobs, err := collectJobs(rows)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, job := range jobs {
		next, ok := smsqueue.Expire(job, maxAge, now)
		if !ok {
			continue
		}
		if err := saveJob(ctx, tx, next); err != nil {
			return 0, err
		}
		changed++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, dbErr("commit expire", err)
	}
	return changed, nil
}

// Cleanup removes old sent or failed SMS rows and consumed OTP rows.
func (db *AppDB) Cleanup(ctx context.Context, before time.Time) error {
	if _, err := db.pool.Exec(ctx, `
		DELETE FROM sms_queue
		WHERE (status = 'sent' AND sent_at IS NOT NULL AND sent_at < $1)
		   OR (status = 'failed' AND created_at < $1)
	`, before); err != nil {
		return dbErr("cleanup sms", err)
	}
	if _, err := db.pool.Exec(ctx, `
		DELETE FROM otp_codes
		WHERE created_at < $1 AND (used OR expires_at < now())
	`, before); err != nil {
		return dbErr("cleanup otp", err)
	}
	return nil
}

// CreateGateway stores a hashed API key. The raw key is not persisted.
func (db *AppDB) CreateGateway(ctx context.Context, name, hash string, prefixes []string) (smsqueue.Gateway, error) {
	if prefixes == nil {
		prefixes = []string{}
	}
	row := db.pool.QueryRow(ctx, `
		INSERT INTO sms_gateways (name, api_key_hash, operator_prefixes)
		VALUES ($1, $2, $3)
		RETURNING id::text, name, operator_prefixes, is_active, last_heartbeat_at, created_at
	`, name, hash, prefixes)
	g, err := scanGateway(row)
	if isUnique(err) {
		return smsqueue.Gateway{}, smsqueue.ErrConflict
	}
	if err != nil {
		return smsqueue.Gateway{}, dbErr("create gateway", err)
	}
	return g, nil
}

// ListGateways returns every gateway without its credential.
func (db *AppDB) ListGateways(ctx context.Context) ([]smsqueue.Gateway, error) {
	return listGateways(ctx, db.pool)
}

// RevokeGateway disables a gateway. Outstanding leases are finished by the reaper.
func (db *AppDB) RevokeGateway(ctx context.Context, id string) error {
	tag, err := db.pool.Exec(ctx, `UPDATE sms_gateways SET is_active = false WHERE id = $1::uuid`, id)
	if err != nil {
		return dbErr("revoke gateway", err)
	}
	if tag.RowsAffected() == 0 {
		return smsqueue.ErrNotFound
	}
	return nil
}

// QueueStats counts jobs by status.
func (db *AppDB) QueueStats(ctx context.Context) (map[string]int, error) {
	rows, err := db.pool.Query(ctx, `SELECT status::text, COUNT(*) FROM sms_queue GROUP BY status`)
	if err != nil {
		return nil, dbErr("stats", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, dbErr("scan stats", err)
		}
		out[status] = n
	}
	return out, rows.Err()
}

const gatewaySelect = `
SELECT id::text, name, operator_prefixes, is_active, last_heartbeat_at, created_at
FROM sms_gateways`

const jobSelect = `
SELECT id::text, phone, message, status::text, attempts, gateway_id::text, last_error,
       locked_until, send_started_at, telephony_accepted_at, delivery_status, sent_at, created_at
FROM sms_queue`

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func scanGateway(row pgx.Row) (smsqueue.Gateway, error) {
	var g smsqueue.Gateway
	var beat *time.Time
	err := row.Scan(&g.ID, &g.Name, &g.Prefixes, &g.Active, &beat, &g.CreatedAt)
	if beat != nil {
		g.LastHeartbeat = *beat
	}
	return g, err
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func listGateways(ctx context.Context, q querier) ([]smsqueue.Gateway, error) {
	rows, err := q.Query(ctx, gatewaySelect+` ORDER BY created_at`)
	if err != nil {
		return nil, dbErr("list gateways", err)
	}
	defer rows.Close()
	var out []smsqueue.Gateway
	for rows.Next() {
		g, err := scanGateway(rows)
		if err != nil {
			return nil, dbErr("scan gateway", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func healthyIDs(ctx context.Context, q querier, since time.Time) (map[string]struct{}, error) {
	rows, err := q.Query(ctx, `
		SELECT id::text FROM sms_gateways
		WHERE is_active AND last_heartbeat_at IS NOT NULL AND last_heartbeat_at > $1
	`, since)
	if err != nil {
		return nil, dbErr("healthy gateways", err)
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, dbErr("scan healthy", err)
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

func collectJobs(rows pgx.Rows) ([]smsqueue.Job, error) {
	defer rows.Close()
	var out []smsqueue.Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, dbErr("scan job", err)
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func scanJob(row pgx.Row) (smsqueue.Job, error) {
	var job smsqueue.Job
	var gatewayID, lastError, delivery *string
	var locked, started, accepted, sent *time.Time
	err := row.Scan(
		&job.ID, &job.Phone, &job.Message, &job.Status, &job.Attempts, &gatewayID, &lastError,
		&locked, &started, &accepted, &delivery, &sent, &job.CreatedAt,
	)
	if err != nil {
		return smsqueue.Job{}, err
	}
	if gatewayID != nil {
		job.GatewayID = *gatewayID
	}
	if lastError != nil {
		job.LastError = *lastError
	}
	if delivery != nil {
		job.DeliveryStatus = *delivery
	}
	if locked != nil {
		job.LockedUntil = *locked
	}
	if started != nil {
		job.SendStartedAt = *started
	}
	if accepted != nil {
		job.TelephonyAcceptedAt = *accepted
	}
	if sent != nil {
		job.SentAt = *sent
	}
	return job, nil
}

func saveJob(ctx context.Context, tx pgx.Tx, job smsqueue.Job) error {
	_, err := tx.Exec(ctx, `
		UPDATE sms_queue SET
			status = $2::sms_status,
			attempts = $3,
			gateway_id = NULLIF($4, '')::uuid,
			last_error = NULLIF($5, ''),
			locked_until = $6,
			send_started_at = $7,
			telephony_accepted_at = $8,
			delivery_status = NULLIF($9, ''),
			sent_at = $10
		WHERE id = $1::uuid
	`, job.ID, job.Status, job.Attempts, job.GatewayID, job.LastError,
		nullTime(job.LockedUntil), nullTime(job.SendStartedAt), nullTime(job.TelephonyAcceptedAt),
		job.DeliveryStatus, nullTime(job.SentAt))
	if err != nil {
		return dbErr("save job", err)
	}
	return nil
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func dbErr(op string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// Message never contains row values; Detail can, so it is left out.
		return fmt.Errorf("%s: sqlstate %s constraint %s: %s", op, pgErr.Code, pgErr.ConstraintName, pgErr.Message)
	}
	return fmt.Errorf("%s: %v", op, err)
}

// queryRower is referenced so a pool and a transaction can both scan one gateway.
var _ queryRower = (*pgxpool.Pool)(nil)
