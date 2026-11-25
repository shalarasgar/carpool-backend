package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/staff"
)

// The rule that one active admin must remain counts every admin in the database, which
// the shared test database cannot isolate; the service tests cover it.
func TestStaffAccountsSessionsAndAudit(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	now := time.Now()
	tag := letterTag("st")

	a, err := db.CreateStaff(ctx, staff.Member{Login: tag + "a", Name: "Admin " + tag, Role: staff.RoleAdmin}, "hash-a", "")
	if err != nil {
		t.Fatal(err)
	}
	m, err := db.CreateStaff(ctx, staff.Member{Login: tag + "m", Name: "Mod", Role: staff.RoleModerator}, "hash-m", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateStaff(ctx, staff.Member{Login: tag + "a", Name: "Again", Role: staff.RoleAdmin}, "x", ""); !errors.Is(err, staff.ErrLoginTaken) {
		t.Fatalf("duplicate login: %v", err)
	}
	got, hash, err := db.StaffCredentials(ctx, tag+"m")
	if err != nil || got.ID != m.ID || hash != "hash-m" || got.Role != staff.RoleModerator {
		t.Fatalf("credentials = %+v %q %v", got, hash, err)
	}
	if _, _, err := db.StaffCredentials(ctx, tag+"nobody"); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("unknown login: %v", err)
	}

	// Sessions: live, expired, and ended by a password change except the one kept.
	for _, s := range []struct {
		hash    string
		expires time.Time
	}{{tag + "s1", now.Add(time.Hour)}, {tag + "s2", now.Add(time.Hour)}, {tag + "old", now.Add(-time.Minute)}} {
		if err := db.CreateStaffSession(ctx, m.ID, s.hash, now, s.expires); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := db.SessionMember(ctx, tag+"s1", now); err != nil || got.ID != m.ID || got.LastLoginAt == nil {
		t.Fatalf("live session = %+v, %v", got, err)
	}
	if _, err := db.SessionMember(ctx, tag+"old", now); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("expired session: %v", err)
	}
	if err := db.SetStaffPassword(ctx, m.ID, "hash-m2", tag+"s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SessionMember(ctx, tag+"s2", now); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("other session after the password change: %v", err)
	}
	if h, _ := db.StaffPasswordHash(ctx, m.ID); h != "hash-m2" {
		t.Fatalf("hash = %q", h)
	}

	// Disabling ends the sessions; enabling does not bring them back.
	yes, no := true, false
	off, err := db.UpdateStaff(ctx, m.ID, staff.Update{Disabled: &yes}, now)
	if err != nil || off.DisabledAt == nil {
		t.Fatalf("disable = %+v, %v", off, err)
	}
	if _, err := db.SessionMember(ctx, tag+"s1", now); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("session of a disabled account: %v", err)
	}
	name, role := "Renamed", staff.RoleAdmin
	on, err := db.UpdateStaff(ctx, m.ID, staff.Update{Disabled: &no, Name: &name, Role: &role}, now)
	if err != nil || on.DisabledAt != nil || on.Name != "Renamed" || on.Role != staff.RoleAdmin {
		t.Fatalf("enable = %+v, %v", on, err)
	}
	if _, err := db.UpdateStaff(ctx, "00000000-0000-4000-8000-000000000000", staff.Update{Name: &name}, now); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("unknown member: %v", err)
	}

	// The audit log, newest first, filtered by actor or by app account.
	user, _ := mkUser(t, db, tag+"User")
	for _, e := range []staff.Entry{
		{StaffID: a.ID, StaffName: a.Name, Action: staff.ActionUserBlock, TargetUserID: user},
		{StaffID: m.ID, StaffName: "Mod", Action: staff.ActionReportResolve, Target: "r1"},
		{StaffName: "ADMIN_TOKEN", Action: staff.ActionUserUnblock, TargetUserID: user, Note: "n"},
	} {
		if err := db.RecordAudit(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	byUser, _, err := db.Audit(ctx, staff.AuditFilter{TargetUserID: user}, 1, 20)
	if err != nil || len(byUser) != 2 || byUser[0].Action != staff.ActionUserUnblock || byUser[0].StaffID != "" ||
		byUser[1].StaffName != a.Name {
		t.Fatalf("by user = %+v, %v", byUser, err)
	}
	page, more, err := db.Audit(ctx, staff.AuditFilter{StaffID: a.ID}, 1, 1)
	if err != nil || len(page) != 1 || more || page[0].TargetUserID != user {
		t.Fatalf("by staff = %+v more=%v %v", page, more, err)
	}
}
