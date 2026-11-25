package server

import (
	"net/http"
	"net/url"
	"testing"
)

// The admin panel's read routes through the real router and PostgreSQL. It runs when
// CARPOOL_TEST_DATABASE_URL is set.
func TestAdminPanelEndToEnd(t *testing.T) {
	driverPhone, passengerPhone := e2ePhone("6"), e2ePhone("7")
	e := startE2E(t, "", driverPhone, passengerPhone)

	driver := e.login(driverPhone, "device-driver")
	driverID := e.call("PATCH", "/v1/me", driver, map[string]string{"first_name": "Döwlet", "last_name": "Admintest"}, http.StatusOK)["id"].(string)
	passenger := e.login(passengerPhone, "device-passenger")
	passengerID := e.call("PATCH", "/v1/me", passenger, map[string]string{"first_name": "Jeren", "last_name": "Admintest"}, http.StatusOK)["id"].(string)
	e.call("POST", "/v1/reports", passenger, map[string]any{"reported_user_id": driverID, "reason": "fraud"}, http.StatusCreated)
	e.call("POST", "/v1/reports", passenger, map[string]any{"reported_user_id": driverID, "reason": "abuse"}, http.StatusCreated)

	// Every admin route needs the admin token, not a user's.
	for _, path := range []string{"/admin/v1/overview", "/admin/v1/users", "/admin/v1/users/" + driverID,
		"/admin/v1/users/" + driverID + "/trips", "/admin/v1/users/" + driverID + "/bookings",
		"/admin/v1/users/" + driverID + "/reviews"} {
		e.call("GET", path, "", nil, http.StatusUnauthorized)
		e.call("GET", path, driver, nil, http.StatusUnauthorized)
	}

	o := e.call("GET", "/admin/v1/overview", e2eAdminToken, nil, http.StatusOK)
	if reports := o["reports"].(map[string]any); reports["open"].(float64) < 2 {
		t.Fatalf("overview = %v", o)
	}
	if users := o["users"].(map[string]any); users["new_today"].(float64) < 2 {
		t.Fatalf("overview = %v", o)
	}
	if _, ok := o["sms"].(map[string]any)["last_heartbeat_at"]; !ok {
		t.Fatalf("overview has no gateway heartbeat field: %v", o)
	}

	// Search by the local part of the number finds the account, phone included.
	found := e.call("GET", "/admin/v1/users?q="+url.QueryEscape(driverPhone[4:]), e2eAdminToken, nil, http.StatusOK)["items"].([]any)
	if len(found) != 1 || found[0].(map[string]any)["id"] != driverID || found[0].(map[string]any)["phone"] != driverPhone {
		t.Fatalf("phone search = %v", found)
	}
	if found[0].(map[string]any)["open_reports"].(float64) != 2 {
		t.Fatalf("open reports in the list = %v", found[0])
	}
	e.call("GET", "/admin/v1/users?status=banned", e2eAdminToken, nil, http.StatusUnprocessableEntity)

	card := e.call("GET", "/admin/v1/users/"+driverID, e2eAdminToken, nil, http.StatusOK)
	activity := card["activity"].(map[string]any)
	if card["phone"] != driverPhone || card["first_name"] != "Döwlet" || activity["reports_against"].(float64) != 2 ||
		activity["reporters"].(float64) != 1 || card["vehicles"] == nil {
		t.Fatalf("card = %v", card)
	}
	e.call("GET", "/admin/v1/users/not-a-uuid", e2eAdminToken, nil, http.StatusNotFound)
	e.call("GET", "/admin/v1/users/00000000-0000-4000-8000-000000000000", e2eAdminToken, nil, http.StatusNotFound)

	for _, tab := range []string{"trips", "bookings", "reviews"} {
		list := e.call("GET", "/admin/v1/users/"+passengerID+"/"+tab+"?scope=history", e2eAdminToken, nil, http.StatusOK)
		if items, ok := list["items"].([]any); !ok || len(items) != 0 || list["has_more"] != false {
			t.Fatalf("%s = %v", tab, list)
		}
	}

	// Report filters, with the reported user's history on each row.
	reports := e.call("GET", "/admin/v1/reports?reported_user_id="+driverID+"&reason=fraud", e2eAdminToken, nil, http.StatusOK)["items"].([]any)
	if len(reports) != 1 {
		t.Fatalf("filtered reports = %v", reports)
	}
	r := reports[0].(map[string]any)
	if r["reporter_id"] != passengerID || r["reported_reports"].(float64) != 2 || r["reported_reporters"].(float64) != 1 {
		t.Fatalf("report = %v", r)
	}
	e.call("GET", "/admin/v1/reports?reason=rude", e2eAdminToken, nil, http.StatusUnprocessableEntity)
	e.call("GET", "/admin/v1/reports?reporter_id=nope", e2eAdminToken, nil, http.StatusUnprocessableEntity)
}
