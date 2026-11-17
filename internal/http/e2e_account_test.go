package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Account deletion, user blocks, reports, the admin moderation routes, and the review
// login, through the real router and PostgreSQL. It runs when CARPOOL_TEST_DATABASE_URL
// is set.
func TestAccountSafetyEndToEnd(t *testing.T) {
	driverPhone, passengerPhone, reviewPhone := e2ePhone("6"), e2ePhone("7"), e2ePhone("6")
	e := startE2E(t, reviewPhone, driverPhone, passengerPhone)

	driver := e.login(driverPhone, "device-driver")
	driverID := e.call("PATCH", "/v1/me", driver, map[string]string{"first_name": "Merdan", "last_name": "Ataýew"}, http.StatusOK)["id"].(string)
	passenger := e.login(passengerPhone, "device-passenger")
	passengerID := e.call("PATCH", "/v1/me", passenger, map[string]string{"first_name": "Aýna", "last_name": "Orazowa"}, http.StatusOK)["id"].(string)

	// Blocks.
	if body := e.call("POST", "/v1/users/"+passengerID+"/block", passenger, nil, http.StatusUnprocessableEntity); errCode(body) != "VALIDATION_ERROR" {
		t.Fatalf("self block = %v", body)
	}
	e.call("POST", "/v1/users/not-a-uuid/block", passenger, nil, http.StatusNotFound)
	e.call("POST", "/v1/users/"+driverID+"/block", passenger, nil, http.StatusNoContent)
	blocks := e.call("GET", "/v1/me/blocks", passenger, nil, http.StatusOK)["items"].([]any)
	if len(blocks) != 1 || blocks[0].(map[string]any)["id"] != driverID {
		t.Fatalf("blocks = %v", blocks)
	}
	if raw, _ := json.Marshal(blocks); bytes.Contains(raw, []byte(driverPhone)) {
		t.Fatal("the block list leaked a phone number")
	}
	e.call("DELETE", "/v1/users/"+driverID+"/block", passenger, nil, http.StatusNoContent)
	if n := len(e.call("GET", "/v1/me/blocks", passenger, nil, http.StatusOK)["items"].([]any)); n != 0 {
		t.Fatalf("blocks after unblock = %d", n)
	}

	// Reports and the moderation queue.
	if body := e.call("POST", "/v1/reports", passenger, map[string]any{"reported_user_id": driverID, "reason": "other"}, http.StatusUnprocessableEntity); errCode(body) != "VALIDATION_ERROR" {
		t.Fatalf("report without details = %v", body)
	}
	report := e.call("POST", "/v1/reports", passenger,
		map[string]any{"reported_user_id": driverID, "reason": "abuse", "details": "was rude on the phone"}, http.StatusCreated)
	reportID, _ := report["id"].(string)
	if reportID == "" {
		t.Fatalf("report = %v", report)
	}
	e.call("GET", "/admin/v1/reports", "", nil, http.StatusUnauthorized)
	e.call("GET", "/admin/v1/reports", driver, nil, http.StatusUnauthorized) // a user token is not the admin token
	findReport := func(status string) map[string]any {
		t.Helper()
		for page := 1; ; page++ {
			list := e.call("GET", fmt.Sprintf("/admin/v1/reports?status=%s&limit=50&page=%d", status, page), e2eAdminToken, nil, http.StatusOK)
			for _, it := range list["items"].([]any) {
				if r := it.(map[string]any); r["id"] == reportID {
					return r
				}
			}
			if list["has_more"] != true {
				return nil
			}
		}
	}
	queued := findReport("open")
	if queued == nil || queued["reported_user_id"] != driverID || queued["reported_name"] != "Merdan Ataýew" ||
		queued["reason"] != "abuse" || queued["details"] != "was rude on the phone" {
		t.Fatalf("queued report = %v", queued)
	}
	if raw, _ := json.Marshal(queued); bytes.Contains(raw, []byte(driverPhone)) || bytes.Contains(raw, []byte(passengerPhone)) {
		t.Fatal("the report leaked a phone number")
	}
	e.call("GET", "/admin/v1/reports?status=closed", e2eAdminToken, nil, http.StatusUnprocessableEntity)
	e.call("POST", "/admin/v1/reports/"+reportID+"/resolve", e2eAdminToken, nil, http.StatusNoContent)
	if findReport("open") != nil || findReport("resolved") == nil {
		t.Fatal("the report did not move from the open queue to the resolved one")
	}

	// A ban takes effect on the next request and is lifted again.
	e.call("POST", "/admin/v1/users/"+driverID+"/block", e2eAdminToken, nil, http.StatusNoContent)
	if body := e.call("GET", "/v1/me", driver, nil, http.StatusForbidden); errCode(body) != "USER_BLOCKED" {
		t.Fatalf("banned user = %v", body)
	}
	e.call("POST", "/admin/v1/users/"+driverID+"/unblock", e2eAdminToken, nil, http.StatusNoContent)
	e.call("GET", "/v1/me", driver, nil, http.StatusOK)

	// The driver publishes a trip, the passenger books it, then the driver deletes the account.
	vehicle := e.call("POST", "/v1/me/vehicles", driver,
		map[string]any{"brand": "Toyota", "model": "Camry", "color": "Ak", "plate": "ag 1234", "passenger_seats": 4}, http.StatusCreated)
	departs := time.Now().Add(50 * time.Hour).UTC().Truncate(time.Minute)
	trip := e.call("POST", "/v1/trips", driver, map[string]any{
		"vehicle_id": vehicle["id"], "departs_at": departs, "arrives_at": departs.Add(6 * time.Hour), "seats_total": 3,
		"stops": []map[string]any{
			{"location_id": "c2000000-0000-4000-8000-000000000001"},
			{"location_id": "c2000000-0000-4000-8000-000000000008"},
		},
	}, http.StatusCreated)
	tripID := trip["id"].(string)
	booking := e.call("POST", "/v1/trips/"+tripID+"/bookings", passenger,
		map[string]any{"pickup_order": 0, "dropoff_order": 1, "seats": 1}, http.StatusCreated)
	bookingID := booking["id"].(string)
	e.call("POST", "/v1/bookings/"+bookingID+"/approve", driver, nil, http.StatusOK)

	e.call("DELETE", "/v1/me", "", nil, http.StatusUnauthorized)
	e.call("DELETE", "/v1/me", driver, nil, http.StatusNoContent)
	// The access token has not expired, but the account behind it is gone.
	e.call("GET", "/v1/me", driver, nil, http.StatusUnauthorized)
	e.call("GET", "/v1/users/"+driverID, passenger, nil, http.StatusNotFound)
	after := e.call("GET", "/v1/bookings/"+bookingID, passenger, nil, http.StatusOK)
	if after["status"] != "cancelled_by_driver" || after["contact_phone"] != nil {
		t.Fatalf("booking after the driver left = %v", after)
	}
	gone := after["trip"].(map[string]any)["driver"].(map[string]any)
	if gone["deleted"] != true || gone["first_name"] != "" || gone["last_name"] != "" {
		t.Fatalf("deleted driver in the booking = %v", gone)
	}
	if raw, _ := json.Marshal(after); bytes.Contains(raw, []byte(driverPhone)) || bytes.Contains(raw, []byte("AG 1234")) {
		t.Fatal("the booking still shows the deleted driver's phone or plate")
	}
	// The same number signs up again as a new, empty account.
	again := e.login(driverPhone, "device-driver")
	if me := e.call("GET", "/v1/me", again, nil, http.StatusOK); me["id"] == driverID || me["profile_complete"] != false {
		t.Fatalf("account after signing up again = %v", me)
	}

	// An operator deletes an account for a request received outside the app.
	e.call("POST", "/admin/v1/users/delete", "", map[string]string{"phone": passengerPhone}, http.StatusUnauthorized)
	e.call("POST", "/admin/v1/users/delete", e2eAdminToken, map[string]string{"phone": "12345"}, http.StatusUnprocessableEntity)
	e.call("POST", "/admin/v1/users/delete", e2eAdminToken, map[string]string{"phone": passengerPhone}, http.StatusNoContent)
	e.call("GET", "/v1/me", passenger, nil, http.StatusUnauthorized)
	e.call("POST", "/admin/v1/users/delete", e2eAdminToken, map[string]string{"phone": passengerPhone}, http.StatusNotFound)

	// The review login: the fixed code works although no SMS was sent.
	e.call("POST", "/v1/auth/request-otp", "", map[string]string{"phone": reviewPhone, "device_id": "review-device"}, http.StatusAccepted)
	e.call("POST", "/v1/auth/verify-otp", "", map[string]string{"phone": reviewPhone, "code": "000000", "device_id": "review-device"}, http.StatusUnauthorized)
	session := e.call("POST", "/v1/auth/verify-otp", "", map[string]string{"phone": reviewPhone, "code": e2eReviewCode, "device_id": "review-device"}, http.StatusOK)
	if token, _ := session["access_token"].(string); token == "" {
		t.Fatalf("review session = %v", session)
	}
}
