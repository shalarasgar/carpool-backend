package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/shalarasgar/carpool-backend/internal/staff"
)

// memStaff is an in-memory StaffStore with the store's last-admin rule.
type memStaff struct {
	members  map[string]staff.Member
	hashes   map[string]string
	sessions map[string]string // token hash -> staff id
	expires  map[string]time.Time
	audit    []staff.Entry
	auditErr error
	next     int
}

func newMemStaff() *memStaff {
	return &memStaff{members: map[string]staff.Member{}, hashes: map[string]string{},
		sessions: map[string]string{}, expires: map[string]time.Time{}}
}

func (m *memStaff) CreateStaff(_ context.Context, in staff.Member, hash, _ string) (staff.Member, error) {
	for _, x := range m.members {
		if x.Login == in.Login {
			return staff.Member{}, staff.ErrLoginTaken
		}
	}
	m.next++
	in.ID = "00000000-0000-4000-8000-00000000000" + string(rune('0'+m.next))
	m.members[in.ID], m.hashes[in.ID] = in, hash
	return in, nil
}

func (m *memStaff) StaffCredentials(_ context.Context, login string) (staff.Member, string, error) {
	for id, x := range m.members {
		if x.Login == login {
			return x, m.hashes[id], nil
		}
	}
	return staff.Member{}, "", staff.ErrNotFound
}

func (m *memStaff) StaffPasswordHash(_ context.Context, id string) (string, error) {
	return m.hashes[id], nil
}

func (m *memStaff) StaffMember(_ context.Context, id string) (staff.Member, error) {
	x, ok := m.members[id]
	if !ok {
		return staff.Member{}, staff.ErrNotFound
	}
	return x, nil
}

func (m *memStaff) ListStaff(context.Context) ([]staff.Member, error) {
	var out []staff.Member
	for _, x := range m.members {
		out = append(out, x)
	}
	return out, nil
}

func (m *memStaff) UpdateStaff(_ context.Context, id string, u staff.Update, now time.Time) (staff.Member, error) {
	x := m.members[id]
	admins := 0
	for _, y := range m.members {
		if y.Role == staff.RoleAdmin && y.DisabledAt == nil {
			admins++
		}
	}
	wasAdmin := x.Role == staff.RoleAdmin && x.DisabledAt == nil
	if wasAdmin && admins <= 1 && ((u.Role != nil && *u.Role != staff.RoleAdmin) || (u.Disabled != nil && *u.Disabled)) {
		return staff.Member{}, staff.ErrLastAdmin
	}
	if u.Name != nil {
		x.Name = *u.Name
	}
	if u.Role != nil {
		x.Role = *u.Role
	}
	if u.Disabled != nil {
		if *u.Disabled {
			x.DisabledAt = &now
		} else {
			x.DisabledAt = nil
		}
	}
	m.members[id] = x
	return x, nil
}

func (m *memStaff) SetStaffPassword(_ context.Context, id, hash, keep string) error {
	m.hashes[id] = hash
	for t, owner := range m.sessions {
		if owner == id && t != keep {
			delete(m.sessions, t)
		}
	}
	return nil
}

func (m *memStaff) CreateStaffSession(_ context.Context, id, tokenHash string, _, expires time.Time) error {
	m.sessions[tokenHash], m.expires[tokenHash] = id, expires
	return nil
}

func (m *memStaff) SessionMember(_ context.Context, tokenHash string, now time.Time) (staff.Member, error) {
	id, ok := m.sessions[tokenHash]
	if !ok || !m.expires[tokenHash].After(now) || m.members[id].DisabledAt != nil {
		return staff.Member{}, staff.ErrNotFound
	}
	return m.members[id], nil
}

func (m *memStaff) DeleteStaffSession(_ context.Context, tokenHash string) error {
	delete(m.sessions, tokenHash)
	return nil
}

func (m *memStaff) DeleteExpiredStaffSessions(context.Context, time.Time) error { return nil }

func (m *memStaff) RecordAudit(_ context.Context, e staff.Entry) error {
	if m.auditErr != nil {
		return m.auditErr
	}
	m.audit = append(m.audit, e)
	return nil
}

func (m *memStaff) Audit(context.Context, staff.AuditFilter, int, int) ([]staff.Entry, bool, error) {
	return m.audit, false, nil
}

func newTestStaff(t *testing.T) (*Staff, *memStaff, *time.Time) {
	t.Helper()
	store := newMemStaff()
	s := NewStaff(store, nil)
	s.cost = bcrypt.MinCost
	clock := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }
	return s, store, &clock
}

func TestStaffLoginAndSessions(t *testing.T) {
	t.Parallel()
	s, store, clock := newTestStaff(t)
	ctx := context.Background()
	admin, err := s.Create(ctx, staff.TokenActor, NewMember{Login: " Muhammet ", Name: "Muhammet", Role: staff.RoleAdmin,
		Password: "correct horse battery"})
	if err != nil || admin.Login != "muhammet" {
		t.Fatalf("create = %+v, %v", admin, err)
	}
	if store.audit[0].StaffName != "ADMIN_TOKEN" || store.audit[0].StaffID != "" || store.audit[0].Action != staff.ActionStaffCreate {
		t.Fatalf("bootstrap audit = %+v", store.audit[0])
	}

	for _, bad := range [][2]string{{"muhammet", "wrong password!"}, {"nobody", "correct horse battery"}} {
		if _, err := s.Login(ctx, bad[0], bad[1]); !errors.Is(err, staff.ErrInvalidCredentials) {
			t.Fatalf("login %q: %v", bad[0], err)
		}
	}
	sess, err := s.Login(ctx, "MUHAMMET", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !sess.ExpiresAt.Equal(clock.Add(StaffSessionTTL)) || sess.Member.ID != admin.ID || len(sess.Token) < 40 {
		t.Fatalf("session = %+v", sess)
	}
	if m, err := s.Authenticate(ctx, sess.Token); err != nil || m.ID != admin.ID {
		t.Fatalf("authenticate = %+v, %v", m, err)
	}
	*clock = clock.Add(StaffSessionTTL + time.Second)
	if _, err := s.Authenticate(ctx, sess.Token); !errors.Is(err, staff.ErrUnauthorized) {
		t.Fatalf("expired session: %v", err)
	}
	if _, err := s.Authenticate(ctx, ""); !errors.Is(err, staff.ErrUnauthorized) {
		t.Fatal("empty token")
	}

	// A disabled account cannot sign in, even with the right password.
	second, _ := s.Create(ctx, staff.TokenActor, NewMember{Login: "aman", Name: "Aman", Role: staff.RoleAdmin,
		Password: "another long pass"})
	disabled := true
	if _, err := s.Update(ctx, ActorOf(admin), second.ID, staff.Update{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login(ctx, "aman", "another long pass"); !errors.Is(err, staff.ErrInvalidCredentials) {
		t.Fatalf("disabled login: %v", err)
	}
}

func TestStaffRules(t *testing.T) {
	t.Parallel()
	s, store, _ := newTestStaff(t)
	ctx := context.Background()
	admin, _ := s.Create(ctx, staff.TokenActor, NewMember{Login: "admin1", Name: "A", Role: staff.RoleAdmin, Password: "long enough pass"})
	mod, _ := s.Create(ctx, staff.TokenActor, NewMember{Login: "mod1", Name: "M", Role: staff.RoleModerator, Password: "long enough pass"})
	a, m := ActorOf(admin), ActorOf(mod)

	// Moderators manage nobody and read no log.
	if _, err := s.List(ctx, m); !errors.Is(err, staff.ErrForbidden) {
		t.Fatalf("moderator list: %v", err)
	}
	if _, err := s.Create(ctx, m, NewMember{Login: "x12", Name: "X", Role: staff.RoleAdmin, Password: "long enough pass"}); !errors.Is(err, staff.ErrForbidden) {
		t.Fatalf("moderator create: %v", err)
	}
	if _, _, err := s.AuditLog(ctx, m, staff.AuditFilter{}, 1, 20); !errors.Is(err, staff.ErrForbidden) {
		t.Fatalf("moderator audit: %v", err)
	}

	// Input checks.
	var field *staff.FieldError
	if _, err := s.Create(ctx, a, NewMember{Login: "ok-login", Name: "X", Role: "owner", Password: "long enough pass"}); !errors.As(err, &field) || field.Field != "role" {
		t.Fatalf("unknown role: %v", err)
	}
	if _, err := s.Create(ctx, a, NewMember{Login: "ok-login", Name: "X", Role: staff.RoleModerator, Password: "short"}); !errors.As(err, &field) || field.Field != "password" {
		t.Fatalf("short password: %v", err)
	}
	if _, err := s.Create(ctx, a, NewMember{Login: "MOD1", Name: "X", Role: staff.RoleModerator, Password: "long enough pass"}); !errors.Is(err, staff.ErrLoginTaken) {
		t.Fatalf("taken login: %v", err)
	}

	// Nobody changes their own role or access; the last admin stays.
	demote := staff.RoleModerator
	if _, err := s.Update(ctx, a, admin.ID, staff.Update{Role: &demote}); !errors.Is(err, staff.ErrSelf) {
		t.Fatalf("self demotion: %v", err)
	}
	if _, err := s.Update(ctx, staff.TokenActor, admin.ID, staff.Update{Role: &demote}); !errors.Is(err, staff.ErrLastAdmin) {
		t.Fatalf("last admin: %v", err)
	}
	name := "  Aman Moderator "
	if out, err := s.Update(ctx, a, admin.ID, staff.Update{Name: &name}); err != nil || out.Name != "Aman Moderator" {
		t.Fatalf("own rename = %+v, %v", out, err)
	}
	promote := staff.RoleAdmin
	if out, err := s.Update(ctx, a, mod.ID, staff.Update{Role: &promote}); err != nil || out.Role != staff.RoleAdmin {
		t.Fatalf("promotion = %+v, %v", out, err)
	}
	last := store.audit[len(store.audit)-1]
	if last.Action != staff.ActionStaffRoleChange || last.StaffID != admin.ID || last.Note != "M (mod1): moderator → admin" {
		t.Fatalf("role audit = %+v", last)
	}
	if _, err := s.Update(ctx, a, "nope", staff.Update{Name: &name}); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("bad id: %v", err)
	}

	// An audit write that fails does not undo or fail the action.
	store.auditErr = errors.New("db down")
	if _, err := s.Update(ctx, a, mod.ID, staff.Update{Role: &demote}); err != nil {
		t.Fatalf("update with a failing audit: %v", err)
	}
}

func TestStaffPasswords(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestStaff(t)
	ctx := context.Background()
	admin, _ := s.Create(ctx, staff.TokenActor, NewMember{Login: "admin1", Name: "A", Role: staff.RoleAdmin, Password: "first password"})
	mod, _ := s.Create(ctx, staff.TokenActor, NewMember{Login: "mod1", Name: "M", Role: staff.RoleModerator, Password: "first password"})
	here, _ := s.Login(ctx, "admin1", "first password")
	other, _ := s.Login(ctx, "admin1", "first password")
	modSess, _ := s.Login(ctx, "mod1", "first password")

	if err := s.ChangeOwnPassword(ctx, ActorOf(admin), here.Token, "wrong one!!", "second password"); !errors.Is(err, staff.ErrInvalidCredentials) {
		t.Fatalf("wrong current: %v", err)
	}
	if err := s.ChangeOwnPassword(ctx, ActorOf(admin), here.Token, "first password", "second password"); err != nil {
		t.Fatal(err)
	}
	// The session in use survives; the other one ends.
	if _, err := s.Authenticate(ctx, here.Token); err != nil {
		t.Fatalf("current session: %v", err)
	}
	if _, err := s.Authenticate(ctx, other.Token); !errors.Is(err, staff.ErrUnauthorized) {
		t.Fatalf("other session: %v", err)
	}
	if _, err := s.Login(ctx, "admin1", "second password"); err != nil {
		t.Fatalf("new password: %v", err)
	}
	if err := s.ChangeOwnPassword(ctx, staff.TokenActor, "", "x", "second password"); !errors.Is(err, staff.ErrForbidden) {
		t.Fatalf("token actor has no password: %v", err)
	}

	// An admin's reset ends every session of the member.
	if err := s.ResetPassword(ctx, ActorOf(mod), admin.ID, "third password"); !errors.Is(err, staff.ErrForbidden) {
		t.Fatalf("moderator reset: %v", err)
	}
	if err := s.ResetPassword(ctx, ActorOf(admin), mod.ID, "reset password"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, modSess.Token); !errors.Is(err, staff.ErrUnauthorized) {
		t.Fatalf("session after reset: %v", err)
	}
}
