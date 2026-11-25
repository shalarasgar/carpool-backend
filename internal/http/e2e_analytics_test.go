package server

import (
	"context"
	"net/http"
	"testing"
)

// Signed-in requests are counted per hour and day, and the admin report shows the
// counts and no user ids. Runs when CARPOOL_TEST_DATABASE_URL is set.
func TestActivityIsCountedAndReported(t *testing.T) {
	aPhone, bPhone := e2ePhone("6"), e2ePhone("7")
	e := startE2E(t, "", aPhone, bPhone)
	a := e.login(aPhone, "device-a")
	b := e.login(bPhone, "device-b")

	for range 3 {
		e.call("GET", "/v1/me", a, nil, http.StatusOK)
	}
	e.call("GET", "/v1/me", b, nil, http.StatusOK)
	e.call("GET", "/v1/me", "", nil, http.StatusUnauthorized) // not a user, not counted
	if err := e.activity.Maintain(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/admin/v1/analytics/activity", "/admin/v1/analytics/activity?days=7"} {
		e.call("GET", path, "", nil, http.StatusUnauthorized)
		e.call("GET", path, a, nil, http.StatusUnauthorized)
	}
	e.call("GET", "/admin/v1/analytics/activity?days=91", e2eAdminToken, nil, http.StatusUnprocessableEntity)

	r := e.call("GET", "/admin/v1/analytics/activity?days=7", e2eAdminToken, nil, http.StatusOK)
	hours := r["hours"].([]any)
	if len(hours) != 48 {
		t.Fatalf("hours = %d", len(hours))
	}
	// The shared test database may hold other tests' users in this hour: at least two.
	if now := hours[47].(map[string]any); now["users"].(float64) < 2 {
		t.Fatalf("current hour = %v", now)
	}
	days := r["days"].([]any)
	if today := days[len(days)-1].(map[string]any); today["users"].(float64) < 2 {
		t.Fatalf("today = %v", today)
	}
	if len(r["heatmap"].([]any)) != 7*24 || r["tracking_since"] == nil || r["timezone"] != "UTC" {
		t.Fatalf("report = %v", r)
	}
	for _, h := range hours {
		if len(h.(map[string]any)) != 2 {
			t.Fatalf("an hour row carries more than its start and count: %v", h)
		}
	}
}
