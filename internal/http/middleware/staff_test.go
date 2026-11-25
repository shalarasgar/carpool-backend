package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/staff"
)

type fakeSessions map[string]staff.Member

func (f fakeSessions) Authenticate(_ context.Context, token string) (staff.Member, error) {
	if m, ok := f[token]; ok {
		return m, nil
	}
	return staff.Member{}, staff.ErrUnauthorized
}

func TestStaffAuthAndRoles(t *testing.T) {
	t.Parallel()
	sessions := fakeSessions{
		"admin-session": {ID: "a", Name: "Aman", Role: staff.RoleAdmin},
		"mod-session":   {ID: "m", Name: "Merdan", Role: staff.RoleModerator},
	}
	var seen staff.Actor
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = StaffFrom(r.Context()) })
	open := StaffAuth("static-admin-token-123", sessions)(ok)
	adminOnly := StaffAuth("static-admin-token-123", sessions)(RequireAdmin(ok))

	call := func(h http.Handler, token string) int {
		req := httptest.NewRequest(http.MethodGet, "/admin/v1/x", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for token, want := range map[string]int{"": 401, "nope": 401, "mod-session": 200, "admin-session": 200} {
		if got := call(open, token); got != want {
			t.Fatalf("open %q = %d, want %d", token, got, want)
		}
	}
	if call(open, "static-admin-token-123"); seen != staff.TokenActor {
		t.Fatalf("static token actor = %+v", seen)
	}
	if call(open, "mod-session"); seen.Name != "Merdan" || seen.Role != staff.RoleModerator {
		t.Fatalf("session actor = %+v", seen)
	}
	for token, want := range map[string]int{"mod-session": 403, "admin-session": 200, "static-admin-token-123": 200} {
		if got := call(adminOnly, token); got != want {
			t.Fatalf("admin-only %q = %d, want %d", token, got, want)
		}
	}
	// Without a session store only the static token opens.
	if got := call(StaffAuth("static-admin-token-123", nil)(ok), "admin-session"); got != 401 {
		t.Fatalf("no session store = %d", got)
	}
}

func TestLimitByIPCountsEachAddress(t *testing.T) {
	t.Parallel()
	h := LimitByIP(&WindowLimiter{Max: 2, Window: time.Minute})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	call := func(addr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/admin/v1/auth/login", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	call("10.0.0.1:1000")
	call("10.0.0.1:2000") // another port, same client
	if rec := call("10.0.0.1:3000"); rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("third attempt = %d", rec.Code)
	}
	if rec := call("10.0.0.2:1000"); rec.Code != http.StatusOK {
		t.Fatalf("other client = %d", rec.Code)
	}
}
