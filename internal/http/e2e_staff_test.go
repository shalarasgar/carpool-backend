package server

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"testing"
)

// Panel accounts end to end: bootstrap with the static token, sign in with a password,
// roles, the audit log, sign-out. Runs when CARPOOL_TEST_DATABASE_URL is set.
func TestStaffAccountsEndToEnd(t *testing.T) {
	userPhone := e2ePhone("6")
	e := startE2E(t, "", userPhone)
	user := e.login(userPhone, "device-u")
	userID := e.call("GET", "/v1/me", user, nil, http.StatusOK)["id"].(string)
	tag := fmt.Sprintf("e2e%06d", rand.IntN(1000000))

	// Without a token nothing opens; a user's token is not a staff token.
	e.call("GET", "/admin/v1/me", "", nil, http.StatusUnauthorized)
	e.call("GET", "/admin/v1/me", user, nil, http.StatusUnauthorized)

	// The static token bootstraps the first accounts.
	me := e.call("GET", "/admin/v1/me", e2eAdminToken, nil, http.StatusOK)
	if me["via_token"] != true || me["role"] != "admin" {
		t.Fatalf("token me = %v", me)
	}
	admin := e.call("POST", "/admin/v1/staff", e2eAdminToken, map[string]any{
		"login": tag + "-admin", "name": "Admin " + tag, "role": "admin", "password": "admin password 1",
	}, http.StatusCreated)
	e.call("POST", "/admin/v1/staff", e2eAdminToken, map[string]any{
		"login": tag + "-mod", "name": "Mod " + tag, "role": "moderator", "password": "short",
	}, http.StatusUnprocessableEntity)
	mod := e.call("POST", "/admin/v1/staff", e2eAdminToken, map[string]any{
		"login": tag + "-mod", "name": "Mod " + tag, "role": "moderator", "password": "moder password 1",
	}, http.StatusCreated)
	e.call("POST", "/admin/v1/staff", e2eAdminToken, map[string]any{
		"login": tag + "-MOD", "name": "Again", "role": "moderator", "password": "moder password 1",
	}, http.StatusConflict)

	// Sign-in.
	e.call("POST", "/admin/v1/auth/login", "", map[string]any{"login": tag + "-mod", "password": "wrong password"}, http.StatusUnauthorized)
	session := e.call("POST", "/admin/v1/auth/login", "", map[string]any{"login": tag + "-ADMIN", "password": "admin password 1"}, http.StatusOK)
	adminToken := session["token"].(string)
	if session["staff"].(map[string]any)["id"] != admin["id"] || session["expires_at"] == nil {
		t.Fatalf("session = %v", session)
	}
	modToken := e.call("POST", "/admin/v1/auth/login", "", map[string]any{"login": tag + "-mod", "password": "moder password 1"}, http.StatusOK)["token"].(string)

	// A moderator handles users and reports, nothing else.
	for _, path := range []string{"/admin/v1/overview", "/admin/v1/reports", "/admin/v1/users", "/admin/v1/analytics/activity"} {
		e.call("GET", path, modToken, nil, http.StatusOK)
	}
	e.call("POST", "/admin/v1/users/"+userID+"/block", modToken, nil, http.StatusNoContent)
	// (This harness mounts no SMS gateway; its routes sit in the same admin-only group.)
	for _, c := range []struct{ method, path string }{
		{"GET", "/admin/v1/staff"}, {"POST", "/admin/v1/staff"}, {"GET", "/admin/v1/audit"},
		{"POST", "/admin/v1/users/delete"}, {"PATCH", "/admin/v1/staff/" + admin["id"].(string)},
	} {
		e.call(c.method, c.path, modToken, map[string]any{}, http.StatusForbidden)
	}

	// The admin's session can do the rest, and sees who did what.
	e.call("POST", "/admin/v1/users/"+userID+"/unblock", adminToken, nil, http.StatusNoContent)
	log := e.call("GET", "/admin/v1/audit?user_id="+userID, adminToken, nil, http.StatusOK)["items"].([]any)
	if len(log) != 2 || log[0].(map[string]any)["action"] != "user.unblock" ||
		log[0].(map[string]any)["staff_name"] != "Admin "+tag || log[1].(map[string]any)["staff_name"] != "Mod "+tag {
		t.Fatalf("audit = %v", log)
	}
	created := e.call("GET", "/admin/v1/audit?action=staff.create&staff_id=", adminToken, nil, http.StatusOK)["items"].([]any)
	if len(created) == 0 || created[0].(map[string]any)["staff_name"] != "ADMIN_TOKEN" {
		t.Fatalf("bootstrap entries = %v", created)
	}

	// Own role and access cannot change; another admin's can.
	e.call("PATCH", "/admin/v1/staff/"+admin["id"].(string), adminToken, map[string]any{"role": "moderator"}, http.StatusConflict)
	e.call("PATCH", "/admin/v1/staff/"+mod["id"].(string), adminToken, map[string]any{"disabled": true}, http.StatusOK)
	e.call("GET", "/admin/v1/overview", modToken, nil, http.StatusUnauthorized) // disabling ended the session
	e.call("POST", "/admin/v1/auth/login", "", map[string]any{"login": tag + "-mod", "password": "moder password 1"}, http.StatusUnauthorized)

	// Own password, then sign-out.
	e.call("POST", "/admin/v1/me/password", adminToken, map[string]any{"current_password": "nope", "new_password": "admin password 2"}, http.StatusUnauthorized)
	e.call("POST", "/admin/v1/me/password", adminToken, map[string]any{"current_password": "admin password 1", "new_password": "admin password 2"}, http.StatusNoContent)
	if got := e.call("GET", "/admin/v1/me", adminToken, nil, http.StatusOK); got["login"] != tag+"-admin" || got["last_login_at"] == nil {
		t.Fatalf("me = %v", got)
	}
	e.call("POST", "/admin/v1/auth/logout", adminToken, nil, http.StatusNoContent)
	e.call("GET", "/admin/v1/me", adminToken, nil, http.StatusUnauthorized)
	e.call("POST", "/admin/v1/auth/login", "", map[string]any{"login": tag + "-admin", "password": "admin password 2"}, http.StatusOK)
}
