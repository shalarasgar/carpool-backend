package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/shalarasgar/carpool-backend/internal/auth"
	"github.com/shalarasgar/carpool-backend/internal/ride"
	"github.com/shalarasgar/carpool-backend/internal/staff"
)

// StaffStore keeps staff accounts, sessions and the audit log.
type StaffStore interface {
	CreateStaff(ctx context.Context, m staff.Member, passwordHash, createdBy string) (staff.Member, error)
	StaffCredentials(ctx context.Context, login string) (staff.Member, string, error)
	StaffPasswordHash(ctx context.Context, id string) (string, error)
	StaffMember(ctx context.Context, id string) (staff.Member, error)
	ListStaff(ctx context.Context) ([]staff.Member, error)
	UpdateStaff(ctx context.Context, id string, u staff.Update, now time.Time) (staff.Member, error)
	SetStaffPassword(ctx context.Context, id, passwordHash, keepSession string) error
	CreateStaffSession(ctx context.Context, staffID, tokenHash string, now, expires time.Time) error
	SessionMember(ctx context.Context, tokenHash string, now time.Time) (staff.Member, error)
	DeleteStaffSession(ctx context.Context, tokenHash string) error
	DeleteExpiredStaffSessions(ctx context.Context, now time.Time) error
	RecordAudit(ctx context.Context, e staff.Entry) error
	Audit(ctx context.Context, f staff.AuditFilter, page, limit int) ([]staff.Entry, bool, error)
}

// StaffSessionTTL is how long a panel sign-in lasts.
const StaffSessionTTL = 12 * time.Hour

// Staff signs operators in and manages their accounts. Every change is written to the
// audit log.
type Staff struct {
	store  StaffStore
	logger *slog.Logger
	now    func() time.Time
	cost   int
	// A real hash compared against when the login is unknown, so a wrong login takes as
	// long as a wrong password.
	dummy []byte
}

// NewStaff returns the service.
func NewStaff(store StaffStore, logger *slog.Logger) *Staff {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Staff{store: store, logger: logger, now: time.Now, cost: bcrypt.DefaultCost}
	s.dummy, _ = bcrypt.GenerateFromPassword([]byte("not-a-real-password"), s.cost)
	return s
}

// StaffSession is a successful panel sign-in.
type StaffSession struct {
	Token     string
	ExpiresAt time.Time
	Member    staff.Member
}

// Login checks the credentials and opens a session.
func (s *Staff) Login(ctx context.Context, login, password string) (StaffSession, error) {
	login = staff.NormalizeLogin(login)
	m, hash, err := s.store.StaffCredentials(ctx, login)
	switch {
	case errors.Is(err, staff.ErrNotFound):
		_ = bcrypt.CompareHashAndPassword(s.dummy, []byte(password))
		return StaffSession{}, staff.ErrInvalidCredentials
	case err != nil:
		return StaffSession{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil || m.DisabledAt != nil {
		return StaffSession{}, staff.ErrInvalidCredentials
	}
	raw, tokenHash, err := auth.NewToken("st_")
	if err != nil {
		return StaffSession{}, err
	}
	now := s.now()
	expires := now.Add(StaffSessionTTL)
	if err := s.store.CreateStaffSession(ctx, m.ID, tokenHash, now, expires); err != nil {
		return StaffSession{}, err
	}
	m.LastLoginAt = &now
	s.Record(ctx, ActorOf(m), staff.Entry{Action: staff.ActionLogin})
	return StaffSession{Token: raw, ExpiresAt: expires, Member: m}, nil
}

// Authenticate returns the member behind a session token.
func (s *Staff) Authenticate(ctx context.Context, token string) (staff.Member, error) {
	if token == "" {
		return staff.Member{}, staff.ErrUnauthorized
	}
	m, err := s.store.SessionMember(ctx, auth.HashSecret(token), s.now())
	if errors.Is(err, staff.ErrNotFound) {
		return staff.Member{}, staff.ErrUnauthorized
	}
	return m, err
}

// Logout ends the session of token.
func (s *Staff) Logout(ctx context.Context, token string) error {
	return s.store.DeleteStaffSession(ctx, auth.HashSecret(token))
}

// Maintain removes expired sessions (run by a jobs.Reaper).
func (s *Staff) Maintain(ctx context.Context) error {
	return s.store.DeleteExpiredStaffSessions(ctx, s.now())
}

// Me returns the actor's own account.
func (s *Staff) Me(ctx context.Context, a staff.Actor) (staff.Member, error) {
	if a.ID == "" {
		return staff.Member{Name: a.Name, Role: a.Role}, nil
	}
	return s.store.StaffMember(ctx, a.ID)
}

// ChangeOwnPassword replaces the actor's password after checking the current one, and
// ends their other sessions. token is the session in use, which stays open.
func (s *Staff) ChangeOwnPassword(ctx context.Context, a staff.Actor, token, current, next string) error {
	if a.ID == "" {
		return staff.ErrForbidden
	}
	hash, err := s.store.StaffPasswordHash(ctx, a.ID)
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
		return staff.ErrInvalidCredentials
	}
	newHash, err := s.hash(next)
	if err != nil {
		return err
	}
	if err := s.store.SetStaffPassword(ctx, a.ID, newHash, auth.HashSecret(token)); err != nil {
		return err
	}
	s.Record(ctx, a, staff.Entry{Action: staff.ActionOwnPassword, Target: a.ID})
	return nil
}

// List returns every account. Admins only.
func (s *Staff) List(ctx context.Context, a staff.Actor) ([]staff.Member, error) {
	if !a.IsAdmin() {
		return nil, staff.ErrForbidden
	}
	return s.store.ListStaff(ctx)
}

// NewMember is the input of Create.
type NewMember struct {
	Login    string
	Name     string
	Role     staff.Role
	Password string
}

// Create adds an account. Admins only.
func (s *Staff) Create(ctx context.Context, a staff.Actor, in NewMember) (staff.Member, error) {
	if !a.IsAdmin() {
		return staff.Member{}, staff.ErrForbidden
	}
	m := staff.Member{Login: staff.NormalizeLogin(in.Login), Name: strings.TrimSpace(in.Name), Role: in.Role}
	if err := staff.ValidateLogin(m.Login); err != nil {
		return staff.Member{}, err
	}
	if err := staff.ValidateName(m.Name); err != nil {
		return staff.Member{}, err
	}
	if !m.Role.Valid() {
		return staff.Member{}, &staff.FieldError{Field: "role", Reason: "unknown"}
	}
	hash, err := s.hash(in.Password)
	if err != nil {
		return staff.Member{}, err
	}
	out, err := s.store.CreateStaff(ctx, m, hash, a.ID)
	if err != nil {
		return staff.Member{}, err
	}
	s.Record(ctx, a, staff.Entry{Action: staff.ActionStaffCreate, Target: out.ID,
		Note: out.Name + " (" + out.Login + "), " + string(out.Role)})
	return out, nil
}

// Update renames, changes the role of, disables or re-enables an account. Admins only;
// nobody changes their own role or access.
func (s *Staff) Update(ctx context.Context, a staff.Actor, id string, p staff.Update) (staff.Member, error) {
	if !a.IsAdmin() {
		return staff.Member{}, staff.ErrForbidden
	}
	if !ride.ValidID(id) {
		return staff.Member{}, staff.ErrNotFound
	}
	if id == a.ID && (p.Role != nil || p.Disabled != nil) {
		return staff.Member{}, staff.ErrSelf
	}
	u := staff.Update{Role: p.Role, Disabled: p.Disabled}
	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		if err := staff.ValidateName(name); err != nil {
			return staff.Member{}, err
		}
		u.Name = &name
	}
	if p.Role != nil && !p.Role.Valid() {
		return staff.Member{}, &staff.FieldError{Field: "role", Reason: "unknown"}
	}
	before, err := s.store.StaffMember(ctx, id)
	if err != nil {
		return staff.Member{}, err
	}
	out, err := s.store.UpdateStaff(ctx, id, u, s.now())
	if err != nil {
		return staff.Member{}, err
	}
	who := out.Name + " (" + out.Login + ")"
	if before.Role != out.Role {
		s.Record(ctx, a, staff.Entry{Action: staff.ActionStaffRoleChange, Target: id,
			Note: who + ": " + string(before.Role) + " → " + string(out.Role)})
	}
	if (before.DisabledAt == nil) != (out.DisabledAt == nil) {
		action := staff.ActionStaffEnable
		if out.DisabledAt != nil {
			action = staff.ActionStaffDisable
		}
		s.Record(ctx, a, staff.Entry{Action: action, Target: id, Note: who})
	}
	if before.Name != out.Name {
		s.Record(ctx, a, staff.Entry{Action: staff.ActionStaffUpdate, Target: id,
			Note: before.Name + " → " + out.Name})
	}
	return out, nil
}

// ResetPassword sets another account's password and ends its sessions. Admins only.
func (s *Staff) ResetPassword(ctx context.Context, a staff.Actor, id, password string) error {
	if !a.IsAdmin() {
		return staff.ErrForbidden
	}
	if !ride.ValidID(id) {
		return staff.ErrNotFound
	}
	hash, err := s.hash(password)
	if err != nil {
		return err
	}
	if err := s.store.SetStaffPassword(ctx, id, hash, ""); err != nil {
		return err
	}
	m, err := s.store.StaffMember(ctx, id)
	if err == nil {
		s.Record(ctx, a, staff.Entry{Action: staff.ActionStaffPassword, Target: id, Note: m.Name + " (" + m.Login + ")"})
	}
	return nil
}

// AuditLog returns the log, newest first. Admins only.
func (s *Staff) AuditLog(ctx context.Context, a staff.Actor, f staff.AuditFilter, page, limit int) ([]staff.Entry, bool, error) {
	if !a.IsAdmin() {
		return nil, false, staff.ErrForbidden
	}
	if (f.StaffID != "" && !ride.ValidID(f.StaffID)) || (f.TargetUserID != "" && !ride.ValidID(f.TargetUserID)) {
		return nil, false, &staff.FieldError{Field: "id", Reason: "format"}
	}
	page, limit = pageBounds(page, limit)
	return s.store.Audit(ctx, f, page, limit)
}

// Record writes an audit entry for a. A failed write is logged, never returned: the
// action it describes already happened.
func (s *Staff) Record(ctx context.Context, a staff.Actor, e staff.Entry) {
	e.StaffID, e.StaffName = a.ID, a.Name
	if err := s.store.RecordAudit(context.WithoutCancel(ctx), e); err != nil {
		s.logger.Error("audit write failed", "action", e.Action, "err", err)
	}
}

func (s *Staff) hash(password string) (string, error) {
	if err := staff.ValidatePassword(password); err != nil {
		return "", err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), s.cost)
	return string(h), err
}

// ActorOf is the actor of a signed-in member.
func ActorOf(m staff.Member) staff.Actor { return staff.Actor{ID: m.ID, Name: m.Name, Role: m.Role} }
