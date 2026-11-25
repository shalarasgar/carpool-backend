package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/shalarasgar/carpool-backend/internal/staff"
)

const staffColumns = `id::text, login, name, role, created_at, last_login_at, disabled_at`

func scanStaff(row pgx.Row) (staff.Member, error) {
	var m staff.Member
	var role string
	err := row.Scan(&m.ID, &m.Login, &m.Name, &role, &m.CreatedAt, &m.LastLoginAt, &m.DisabledAt)
	m.Role = staff.Role(role)
	return m, err
}

func staffErr(op string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
		return staff.ErrNotFound
	}
	if isUnique(err) {
		return staff.ErrLoginTaken
	}
	return dbErr(op, err)
}

// CountStaff returns how many staff accounts exist, disabled ones included.
func (db *AppDB) CountStaff(ctx context.Context) (int, error) {
	var n int
	if err := db.pool.QueryRow(ctx, `SELECT COUNT(*) FROM staff`).Scan(&n); err != nil {
		return 0, dbErr("count staff", err)
	}
	return n, nil
}

// CreateStaff inserts an account. createdBy is empty for the ADMIN_TOKEN.
func (db *AppDB) CreateStaff(ctx context.Context, m staff.Member, passwordHash, createdBy string) (staff.Member, error) {
	row := db.pool.QueryRow(ctx, `
		INSERT INTO staff (login, name, role, password_hash, created_by)
		VALUES ($1, $2, $3, $4, NULLIF($5, '')::uuid)
		RETURNING `+staffColumns,
		m.Login, m.Name, string(m.Role), passwordHash, createdBy)
	out, err := scanStaff(row)
	if err != nil {
		return staff.Member{}, staffErr("create staff", err)
	}
	return out, nil
}

// StaffCredentials returns the account with this login and its password hash.
func (db *AppDB) StaffCredentials(ctx context.Context, login string) (staff.Member, string, error) {
	var hash string
	var m staff.Member
	var role string
	err := db.pool.QueryRow(ctx, `SELECT `+staffColumns+`, password_hash FROM staff WHERE login = $1`, login).
		Scan(&m.ID, &m.Login, &m.Name, &role, &m.CreatedAt, &m.LastLoginAt, &m.DisabledAt, &hash)
	if err != nil {
		return staff.Member{}, "", staffErr("staff credentials", err)
	}
	m.Role = staff.Role(role)
	return m, hash, nil
}

// StaffPasswordHash returns the password hash of an account.
func (db *AppDB) StaffPasswordHash(ctx context.Context, id string) (string, error) {
	var hash string
	if err := db.pool.QueryRow(ctx, `SELECT password_hash FROM staff WHERE id = $1::uuid`, id).Scan(&hash); err != nil {
		return "", staffErr("staff password", err)
	}
	return hash, nil
}

// StaffMember returns one account.
func (db *AppDB) StaffMember(ctx context.Context, id string) (staff.Member, error) {
	m, err := scanStaff(db.pool.QueryRow(ctx, `SELECT `+staffColumns+` FROM staff WHERE id = $1::uuid`, id))
	if err != nil {
		return staff.Member{}, staffErr("staff member", err)
	}
	return m, nil
}

// ListStaff returns every account: active ones first, then by name.
func (db *AppDB) ListStaff(ctx context.Context) ([]staff.Member, error) {
	rows, err := db.pool.Query(ctx, `SELECT `+staffColumns+` FROM staff ORDER BY disabled_at IS NOT NULL, lower(name), login`)
	if err != nil {
		return nil, dbErr("list staff", err)
	}
	defer rows.Close()
	var out []staff.Member
	for rows.Next() {
		m, err := scanStaff(rows)
		if err != nil {
			return nil, dbErr("scan staff", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateStaff applies u in one transaction. It refuses (ErrLastAdmin) a change that
// would leave no active admin, and ends the sessions of a disabled account.
func (db *AppDB) UpdateStaff(ctx context.Context, id string, u staff.Update, now time.Time) (staff.Member, error) {
	var out staff.Member
	err := db.inTx(ctx, "update staff", func(tx pgx.Tx) error {
		// Lock every active admin, so two concurrent demotions cannot both pass the check.
		rows, err := tx.Query(ctx, `SELECT id::text FROM staff WHERE role = 'admin' AND disabled_at IS NULL FOR UPDATE`)
		if err != nil {
			return dbErr("lock admins", err)
		}
		admins := map[string]bool{}
		for rows.Next() {
			var a string
			if err := rows.Scan(&a); err != nil {
				rows.Close()
				return dbErr("scan admins", err)
			}
			admins[a] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return dbErr("lock admins", err)
		}

		cur, err := scanStaff(tx.QueryRow(ctx, `SELECT `+staffColumns+` FROM staff WHERE id = $1::uuid FOR UPDATE`, id))
		if err != nil {
			return staffErr("staff member", err)
		}
		losesAdmin := admins[cur.ID] &&
			((u.Role != nil && *u.Role != staff.RoleAdmin) || (u.Disabled != nil && *u.Disabled))
		if losesAdmin && len(admins) <= 1 {
			return staff.ErrLastAdmin
		}

		name, role, disabledAt := cur.Name, cur.Role, cur.DisabledAt
		if u.Name != nil {
			name = *u.Name
		}
		if u.Role != nil {
			role = *u.Role
		}
		if u.Disabled != nil {
			switch {
			case *u.Disabled && disabledAt == nil:
				disabledAt = &now
			case !*u.Disabled:
				disabledAt = nil
			}
		}
		out, err = scanStaff(tx.QueryRow(ctx, `
			UPDATE staff SET name = $2, role = $3, disabled_at = $4 WHERE id = $1::uuid
			RETURNING `+staffColumns, id, name, string(role), disabledAt))
		if err != nil {
			return staffErr("update staff", err)
		}
		if disabledAt != nil {
			if _, err := tx.Exec(ctx, `DELETE FROM staff_sessions WHERE staff_id = $1::uuid`, id); err != nil {
				return dbErr("end staff sessions", err)
			}
		}
		return nil
	})
	return out, err
}

// SetStaffPassword replaces the password and ends every session of the account except
// keepSession (a token hash, or empty to end them all).
func (db *AppDB) SetStaffPassword(ctx context.Context, id, passwordHash, keepSession string) error {
	return db.inTx(ctx, "set staff password", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE staff SET password_hash = $2 WHERE id = $1::uuid`, id, passwordHash)
		if err != nil {
			return staffErr("set staff password", err)
		}
		if tag.RowsAffected() == 0 {
			return staff.ErrNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM staff_sessions WHERE staff_id = $1::uuid AND token_hash <> $2`,
			id, keepSession); err != nil {
			return dbErr("end staff sessions", err)
		}
		return nil
	})
}

// CreateStaffSession stores a session and the time of the login.
func (db *AppDB) CreateStaffSession(ctx context.Context, staffID, tokenHash string, now, expires time.Time) error {
	return db.inTx(ctx, "create staff session", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO staff_sessions (token_hash, staff_id, created_at, expires_at) VALUES ($1, $2::uuid, $3, $4)`,
			tokenHash, staffID, now, expires); err != nil {
			return dbErr("create staff session", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE staff SET last_login_at = $2 WHERE id = $1::uuid`, staffID, now); err != nil {
			return dbErr("record staff login", err)
		}
		return nil
	})
}

// SessionMember returns the active account of a live session.
func (db *AppDB) SessionMember(ctx context.Context, tokenHash string, now time.Time) (staff.Member, error) {
	m, err := scanStaff(db.pool.QueryRow(ctx, `
		SELECT s.id::text, s.login, s.name, s.role, s.created_at, s.last_login_at, s.disabled_at
		FROM staff_sessions ss JOIN staff s ON s.id = ss.staff_id
		WHERE ss.token_hash = $1 AND ss.expires_at > $2 AND s.disabled_at IS NULL`, tokenHash, now))
	if err != nil {
		return staff.Member{}, staffErr("staff session", err)
	}
	return m, nil
}

// DeleteStaffSession ends one session.
func (db *AppDB) DeleteStaffSession(ctx context.Context, tokenHash string) error {
	if _, err := db.pool.Exec(ctx, `DELETE FROM staff_sessions WHERE token_hash = $1`, tokenHash); err != nil {
		return dbErr("delete staff session", err)
	}
	return nil
}

// DeleteExpiredStaffSessions removes sessions past their expiry.
func (db *AppDB) DeleteExpiredStaffSessions(ctx context.Context, now time.Time) error {
	if _, err := db.pool.Exec(ctx, `DELETE FROM staff_sessions WHERE expires_at <= $1`, now); err != nil {
		return dbErr("delete expired staff sessions", err)
	}
	return nil
}

// RecordAudit appends an entry. Empty ids are stored as NULL.
func (db *AppDB) RecordAudit(ctx context.Context, e staff.Entry) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO staff_audit (staff_id, staff_name, action, target_user_id, target, note)
		VALUES (NULLIF($1, '')::uuid, $2, $3, NULLIF($4, '')::uuid, NULLIF($5, ''), NULLIF($6, ''))`,
		e.StaffID, e.StaffName, e.Action, e.TargetUserID, e.Target, e.Note)
	if err != nil {
		return dbErr("record audit", err)
	}
	return nil
}

// Audit returns the log, newest first.
func (db *AppDB) Audit(ctx context.Context, f staff.AuditFilter, page, limit int) ([]staff.Entry, bool, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, COALESCE(staff_id::text, ''), staff_name, action, COALESCE(target_user_id::text, ''),
		       COALESCE(target, ''), COALESCE(note, ''), created_at
		FROM staff_audit
		WHERE ($1 = '' OR staff_id = NULLIF($1, '')::uuid)
		  AND ($2 = '' OR target_user_id = NULLIF($2, '')::uuid)
		  AND ($3 = '' OR action = $3)
		ORDER BY created_at DESC, id DESC
		LIMIT $4 OFFSET $5`,
		f.StaffID, f.TargetUserID, f.Action, limit+1, (page-1)*limit)
	if err != nil {
		return nil, false, dbErr("audit", err)
	}
	defer rows.Close()
	var out []staff.Entry
	for rows.Next() {
		var e staff.Entry
		if err := rows.Scan(&e.ID, &e.StaffID, &e.StaffName, &e.Action, &e.TargetUserID, &e.Target, &e.Note,
			&e.CreatedAt); err != nil {
			return nil, false, dbErr("scan audit", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, false, dbErr("audit", err)
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}
