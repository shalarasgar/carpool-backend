package server

// End-to-end test of the product API through the real router and PostgreSQL.
// It runs when CARPOOL_TEST_DATABASE_URL is set.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/media"
	appmigrate "github.com/shalarasgar/carpool-backend/internal/migrate"
	"github.com/shalarasgar/carpool-backend/internal/service"
	"github.com/shalarasgar/carpool-backend/internal/store"
)

type e2e struct {
	t   *testing.T
	srv *httptest.Server
}

func (e e2e) call(method, path, token string, body any, wantStatus int) map[string]any {
	e.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "ru")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != wantStatus {
		e.t.Fatalf("%s %s = %d %v, want %d", method, path, resp.StatusCode, out, wantStatus)
	}
	return out
}

func (e e2e) login(phone, device string) string {
	e.t.Helper()
	otp := e.call("POST", "/v1/auth/request-otp", "", map[string]string{"phone": phone, "device_id": device}, http.StatusAccepted)
	code, _ := otp["dev_code"].(string)
	session := e.call("POST", "/v1/auth/verify-otp", "", map[string]string{"phone": phone, "code": code, "device_id": device}, http.StatusOK)
	return session["access_token"].(string)
}

const (
	e2eAdminToken = "e2e-admin-token-0123456789"
	// The fixed code of the review login in these tests.
	e2eReviewCode = "482915"
)

func e2ePhone(prefix string) string {
	return fmt.Sprintf("+993%s%07d", prefix, rand.IntN(10000000))
}

// startE2E serves the real router over the test database. phones skip the OTP limits;
// reviewPhone, when set, is the app-store review login.
func startE2E(t *testing.T, reviewPhone string, phones ...string) e2e {
	t.Helper()
	url := os.Getenv("CARPOOL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("CARPOOL_TEST_DATABASE_URL is not set")
	}
	if err := appmigrate.Up(url); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	db := store.NewAppDB(pool)
	testPhones := map[string]bool{}
	for _, p := range phones {
		testPhones[p] = true
	}
	authCfg := service.AuthConfig{
		AppName: "carpool", OTPSecret: "e2e-otp-secret-0123456789", JWTSecret: "e2e-jwt-secret-0123456789",
		Prefixes: []string{"6", "7"}, OTPTTL: 5 * time.Minute, MaxAttempts: 5, PhoneInterval: time.Minute,
		PhoneDailyMax: 5, IPHourlyMax: 100, DeviceHourlyMax: 100, AccessTTL: 15 * time.Minute,
		RefreshTTL: time.Hour, ReturnDevCode: true, TestPhones: testPhones,
	}
	if reviewPhone != "" {
		authCfg.ReviewPhone, authCfg.ReviewCode = reviewPhone, e2eReviewCode
	}
	authSvc := service.NewAuth(db, authCfg, nil)
	trips := service.NewTrips(db, service.TripConfig{OverlapBuffer: 30 * time.Minute}, nil)
	router := NewRouter(Deps{
		Auth: authSvc, DevMode: true, JWTSecret: "e2e-jwt-secret-0123456789", Users: db,
		AdminToken: e2eAdminToken,
		Profiles:   service.NewProfiles(db, media.Disk{Dir: t.TempDir()}, authSvc, nil),
		Trips:      trips, Bookings: service.NewBookings(db), Moderation: service.NewModeration(db),
		Limiter: &middleware.WindowLimiter{Max: 1000, Window: time.Minute}, MediaDir: t.TempDir(),
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return e2e{t: t, srv: srv}
}

func errCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func TestProductFlowEndToEnd(t *testing.T) {
	driverPhone, passengerPhone := e2ePhone("6"), e2ePhone("7")
	e := startE2E(t, "", driverPhone, passengerPhone)

	e.call("GET", "/v1/me", "", nil, http.StatusUnauthorized)
	locs := e.call("GET", "/v1/locations", "", nil, http.StatusOK)
	if len(locs["items"].([]any)) < 10 {
		t.Fatalf("locations = %v", locs)
	}

	driver := e.login(driverPhone, "device-driver")
	me := e.call("GET", "/v1/me", driver, nil, http.StatusOK)
	if me["profile_complete"] != false {
		t.Fatalf("new user profile_complete = %v", me["profile_complete"])
	}
	vehicleBody := map[string]any{"brand": "Toyota", "model": "Camry", "color": "Ak", "plate": "ag 1234", "passenger_seats": 4}
	// Trip publication requires a complete profile.
	vehicle := e.call("POST", "/v1/me/vehicles", driver, vehicleBody, http.StatusCreated)
	departs := time.Now().Add(50 * time.Hour).UTC().Truncate(time.Minute)
	tripBody := map[string]any{
		"vehicle_id": vehicle["id"], "departs_at": departs, "arrives_at": departs.Add(6 * time.Hour), "seats_total": 3, "price": 150,
		"amenities": map[string]string{"air_conditioning": "yes", "smoking_policy": "prohibited"},
		"stops": []map[string]any{
			{"location_id": "c2000000-0000-4000-8000-000000000001", "meeting_point": "Teke bazar"},
			{"location_id": "c2000000-0000-4000-8000-000000000003"},
			{"location_id": "c2000000-0000-4000-8000-000000000008"},
		},
	}
	if errBody := e.call("POST", "/v1/trips", driver, tripBody, http.StatusUnprocessableEntity); errBody["error"].(map[string]any)["code"] != "PROFILE_INCOMPLETE" {
		t.Fatalf("incomplete profile error = %v", errBody)
	}
	e.call("PATCH", "/v1/me", driver, map[string]string{"first_name": "Merdan", "last_name": "Ataýew"}, http.StatusOK)
	trip := e.call("POST", "/v1/trips", driver, tripBody, http.StatusCreated)
	tripID := trip["id"].(string)
	overlap := e.call("POST", "/v1/trips", driver, tripBody, http.StatusConflict)
	if overlap["error"].(map[string]any)["code"] != "TRIP_OVERLAP" {
		t.Fatalf("overlap = %v", overlap)
	}

	passenger := e.login(passengerPhone, "device-passenger")
	e.call("PATCH", "/v1/me", passenger, map[string]string{"first_name": "Aýna", "last_name": "Orazowa"}, http.StatusOK)
	found := e.call("GET", "/v1/trips?from=a1000000-0000-4000-8000-000000000001&to=c2000000-0000-4000-8000-000000000008&seats=2&air_conditioning=yes", passenger, nil, http.StatusOK)
	ok := false
	for _, it := range found["items"].([]any) {
		if it.(map[string]any)["id"] == tripID {
			ok = true
		}
	}
	if !ok {
		t.Fatal("search did not find the trip from Ahal welayat")
	}
	view := e.call("GET", "/v1/trips/"+tripID, passenger, nil, http.StatusOK)
	if v := view["vehicle"].(map[string]any); v["plate"] != nil {
		t.Fatalf("plate visible before booking: %v", v)
	}

	booking := e.call("POST", "/v1/trips/"+tripID+"/bookings", passenger,
		map[string]any{"pickup_order": 1, "dropoff_order": 2, "seats": 2, "luggage": "small", "message": "Salam"}, http.StatusCreated)
	bookingID := booking["id"].(string)
	if booking["contact_phone"] != nil {
		t.Fatal("pending booking returned a phone")
	}
	e.call("GET", "/v1/bookings/"+bookingID+"/contact", passenger, nil, http.StatusForbidden)
	e.call("POST", "/v1/bookings/"+bookingID+"/approve", passenger, nil, http.StatusForbidden)

	requests := e.call("GET", "/v1/trips/"+tripID+"/bookings", driver, nil, http.StatusOK)
	if n := len(requests["items"].([]any)); n != 1 {
		t.Fatalf("driver sees %d requests", n)
	}
	e.call("GET", "/v1/trips/"+tripID+"/bookings", passenger, nil, http.StatusForbidden)
	approved := e.call("POST", "/v1/bookings/"+bookingID+"/approve", driver, nil, http.StatusOK)
	if approved["status"] != "approved" || approved["contact_phone"] != passengerPhone {
		t.Fatalf("approved booking for driver = %v", approved)
	}
	contact := e.call("GET", "/v1/bookings/"+bookingID+"/contact", passenger, nil, http.StatusOK)
	if contact["phone"] != driverPhone {
		t.Fatalf("passenger contact = %v", contact)
	}
	view = e.call("GET", "/v1/trips/"+tripID, passenger, nil, http.StatusOK)
	if view["vehicle"].(map[string]any)["plate"] != "AG 1234" {
		t.Fatalf("approved passenger plate = %v", view["vehicle"])
	}
	seg := view["free_seats_by_segment"].([]any)
	if seg[0].(float64) != 3 || seg[1].(float64) != 1 {
		t.Fatalf("free seats by segment = %v", seg)
	}

	unread := e.call("GET", "/v1/me/notifications/unread-count", passenger, nil, http.StatusOK)
	if unread["unread"].(float64) < 1 {
		t.Fatalf("passenger unread = %v", unread)
	}
	inbox := e.call("GET", "/v1/me/notifications", passenger, nil, http.StatusOK)
	first := inbox["items"].([]any)[0].(map[string]any)
	if first["type"] != "booking_approved" || first["booking_id"] != bookingID {
		t.Fatalf("inbox = %v", first)
	}
	raw, _ := json.Marshal(inbox)
	if bytes.Contains(raw, []byte(driverPhone)) {
		t.Fatal("notifications leaked a phone number")
	}
	e.call("POST", "/v1/me/notifications/read", passenger, map[string]any{}, http.StatusNoContent)

	upcoming := e.call("GET", "/v1/me/bookings?scope=upcoming", passenger, nil, http.StatusOK)
	if len(upcoming["items"].([]any)) != 1 {
		t.Fatalf("upcoming bookings = %v", upcoming)
	}
	mine := e.call("GET", "/v1/me/trips?scope=upcoming", driver, nil, http.StatusOK)
	if mt := mine["items"].([]any)[0].(map[string]any); mt["approved_seats"].(float64) != 2 {
		t.Fatalf("driver trip counts = %v", mt)
	}
	e.call("POST", "/v1/bookings/"+bookingID+"/review", passenger, map[string]any{"rating": 5}, http.StatusUnprocessableEntity)
	cancelled := e.call("POST", "/v1/bookings/"+bookingID+"/cancel", passenger, nil, http.StatusOK)
	if cancelled["status"] != "cancelled_by_passenger" || cancelled["contact_phone"] != nil {
		t.Fatalf("cancelled = %v", cancelled)
	}
	e.call("POST", "/v1/bookings/"+bookingID+"/cancel", passenger, nil, http.StatusOK)
	e.call("POST", "/v1/trips/"+tripID+"/cancel", driver, nil, http.StatusOK)
	e.call("GET", "/v1/trips/not-a-uuid", driver, nil, http.StatusNotFound)
}
