package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	locAshgabat    = "c2000000-0000-4000-8000-000000000001"
	locAnau        = "c2000000-0000-4000-8000-000000000002"
	locMaryCity    = "c2000000-0000-4000-8000-000000000008"
	locBayramaly   = "c2000000-0000-4000-8000-000000000009"
	locAhal        = "a1000000-0000-4000-8000-000000000001"
	locMaryWelayat = "a1000000-0000-4000-8000-000000000005"
	locHiddenEtrap = "e3000000-0000-4000-8000-000000000099"
)

func TestSearchWelayatBlocksAndPaging(t *testing.T) {
	e := newEnv(t)
	driver, _ := e.signIn("+99365112001", "driver-search")
	other, _ := e.signIn("+99365112002", "other-search")
	viewer, _ := e.signIn("+99365112003", "viewer-search")

	mary := e.createTrip(driver.access, locAshgabat, locMaryCity, 3, 30*time.Hour)
	bayramaly := e.createTrip(driver.access, locAshgabat, locBayramaly, 2, 40*time.Hour)
	welayat := e.createTrip(driver.access, locAshgabat, locMaryWelayat, 2, 50*time.Hour)
	anau := e.createTrip(other.access, locAnau, locAshgabat, 2, 36*time.Hour)
	full := e.createTrip(driver.access, locAshgabat, locMaryCity, 1, 60*time.Hour)
	past := e.createTrip(driver.access, locAshgabat, locMaryCity, 2, 70*time.Hour)
	e.exec(`UPDATE trips SET departs_at = now() - interval '1 hour' WHERE id = $1`, past)

	e.exec(`
		INSERT INTO locations (id, parent_id, type, name_tk, name_ru, name_en, is_active, sort_order)
		VALUES ($1, $2, 'etrap', 'Gizlin', 'Скрытый', 'Hidden', false, 99)
	`, locHiddenEtrap, locMaryWelayat)
	e.exec(`
		INSERT INTO trips (driver_id, from_location_id, to_location_id, departs_at, seats_total, seats_left)
		VALUES ($1, $2, $3, now() + interval '2 days', 2, 2)
	`, driver.userID, locAshgabat, locHiddenEtrap)
	hidden := e.queryString(`SELECT id::text FROM trips WHERE to_location_id = $1`, locHiddenEtrap)

	booked := e.call(http.MethodPost, "/v1/trips/"+full+"/bookings", map[string]any{"seats": 1}, authEN(viewer.access))
	require.Equal(t, http.StatusCreated, booked.Status, string(booked.Body))
	approved := e.call(http.MethodPost, "/v1/bookings/"+booked.str("id")+"/approve", nil, authEN(driver.access))
	require.Equal(t, http.StatusOK, approved.Status, string(approved.Body))

	res := e.call(http.MethodGet, "/v1/trips?from="+locAshgabat+"&to="+locMaryWelayat+"&page_size=10", nil, authEN(viewer.access))
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	ids := tripIDs(t, res)
	require.ElementsMatch(t, []string{mary, bayramaly, welayat}, ids)
	require.NotContains(t, ids, hidden)
	require.NotContains(t, ids, full)
	require.NotContains(t, ids, past)
	require.NotContains(t, ids, anau)
	require.NotContains(t, string(res.Body), "+993")
	require.NotContains(t, string(res.Body), `"phone"`)
	first := res.decoded["trips"].([]any)[0].(map[string]any)
	from := first["from"].(map[string]any)
	require.Equal(t, "Ashgabat", from["name"])
	_, driverHasPhone := first["driver"].(map[string]any)["phone"]
	require.False(t, driverHasPhone)

	city := e.call(http.MethodGet, "/v1/trips?to="+locMaryCity, nil, authEN(viewer.access))
	require.Equal(t, http.StatusOK, city.Status, string(city.Body))
	require.Equal(t, []string{mary}, tripIDs(t, city))

	ahal := e.call(http.MethodGet, "/v1/trips?from="+locAhal+"&to="+locAshgabat, nil, authEN(viewer.access))
	require.Equal(t, http.StatusOK, ahal.Status, string(ahal.Body))
	require.Equal(t, []string{anau}, tripIDs(t, ahal))

	page := e.call(http.MethodGet, "/v1/trips?from="+locAshgabat+"&to="+locMaryWelayat+"&page_size=1&page=1", nil, authEN(viewer.access))
	require.Equal(t, http.StatusOK, page.Status, string(page.Body))
	require.Equal(t, true, page.field("has_more"))
	require.Len(t, tripIDs(t, page), 1)
	page2 := e.call(http.MethodGet, "/v1/trips?from="+locAshgabat+"&to="+locMaryWelayat+"&page_size=1&page=2", nil, authEN(viewer.access))
	require.NotEqual(t, tripIDs(t, page)[0], tripIDs(t, page2)[0])

	seats := e.call(http.MethodGet, "/v1/trips?to="+locBayramaly+"&seats=3", nil, authEN(viewer.access))
	require.Empty(t, tripIDs(t, seats))

	e.exec(`INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`, viewer.userID, driver.userID)
	blocked := e.call(http.MethodGet, "/v1/trips?from="+locAshgabat+"&to="+locMaryWelayat, nil, authEN(viewer.access))
	require.Empty(t, tripIDs(t, blocked))
	hiddenGet := e.call(http.MethodGet, "/v1/trips/"+mary, nil, authEN(viewer.access))
	require.Equal(t, http.StatusNotFound, hiddenGet.Status)
	require.Equal(t, "TRIP_NOT_FOUND", hiddenGet.Err.Error.Code)
	own := e.call(http.MethodGet, "/v1/trips/"+mary, nil, authEN(driver.access))
	require.Equal(t, http.StatusOK, own.Status, string(own.Body))
	still := e.call(http.MethodGet, "/v1/trips?from="+locAhal, nil, authEN(viewer.access))
	require.Equal(t, []string{anau}, tripIDs(t, still))

	e.exec(`INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`, other.userID, viewer.userID)
	reverse := e.call(http.MethodGet, "/v1/trips?from="+locAnau, nil, authEN(viewer.access))
	require.Empty(t, tripIDs(t, reverse))
	denied := e.call(http.MethodPost, "/v1/trips/"+anau+"/bookings", map[string]any{"seats": 1}, authEN(viewer.access))
	require.Equal(t, http.StatusForbidden, denied.Status)
	require.Equal(t, "FORBIDDEN", denied.Err.Error.Code)
}

func TestBookingPhonesCancelAndReopen(t *testing.T) {
	e := newEnv(t)
	driver, _ := e.signIn("+99365113001", "driver-book")
	passenger, _ := e.signIn("+99365113002", "passenger-book")
	second, _ := e.signIn("+99365113003", "passenger-two")

	tripID := e.createTrip(driver.access, locAshgabat, locMaryCity, 2, 48*time.Hour)
	own := e.call(http.MethodPost, "/v1/trips/"+tripID+"/bookings", map[string]any{"seats": 1}, authEN(driver.access))
	require.Equal(t, http.StatusForbidden, own.Status)
	require.Equal(t, "FORBIDDEN", own.Err.Error.Code)

	created := e.call(http.MethodPost, "/v1/trips/"+tripID+"/bookings", map[string]any{"seats": 1}, authEN(passenger.access))
	require.Equal(t, http.StatusCreated, created.Status, string(created.Body))
	bookingID := created.str("id")
	require.NotContains(t, string(created.Body), "+993")
	require.NotContains(t, string(created.Body), `"phone"`)

	again := e.call(http.MethodPost, "/v1/trips/"+tripID+"/bookings", map[string]any{"seats": 1}, authEN(passenger.access))
	require.Equal(t, http.StatusConflict, again.Status)
	require.Equal(t, "BOOKING_EXISTS", again.Err.Error.Code)

	mine := e.call(http.MethodGet, "/v1/me/bookings", nil, authEN(passenger.access))
	require.Equal(t, http.StatusOK, mine.Status, string(mine.Body))
	require.NotContains(t, string(mine.Body), "+993")
	driverList := e.call(http.MethodGet, "/v1/trips/"+tripID+"/bookings", nil, authEN(passenger.access))
	require.Equal(t, http.StatusForbidden, driverList.Status)
	driverList = e.call(http.MethodGet, "/v1/trips/"+tripID+"/bookings", nil, authEN(driver.access))
	require.Equal(t, http.StatusOK, driverList.Status, string(driverList.Body))
	require.NotContains(t, string(driverList.Body), "+993")
	trip := e.call(http.MethodGet, "/v1/trips/"+tripID, nil, authEN(passenger.access))
	require.NotContains(t, string(trip.Body), "+993")

	approved := e.call(http.MethodPost, "/v1/bookings/"+bookingID+"/approve", nil, authEN(driver.access))
	require.Equal(t, http.StatusOK, approved.Status, string(approved.Body))
	require.Equal(t, "approved", approved.str("status"))
	require.Equal(t, "+99365113002", approved.decoded["passenger"].(map[string]any)["phone"])
	_, driverSeesOwn := approved.decoded["driver_phone"]
	require.False(t, driverSeesOwn)

	mine = e.call(http.MethodGet, "/v1/me/bookings", nil, authEN(passenger.access))
	require.Contains(t, string(mine.Body), "+99365113001")
	require.NotContains(t, string(mine.Body), "+99365113002")
	row := mine.decoded["bookings"].([]any)[0].(map[string]any)
	require.Equal(t, "+99365113001", row["driver_phone"])
	_, passengerPhone := row["passenger"].(map[string]any)["phone"]
	require.False(t, passengerPhone)
	nested := row["trip"].(map[string]any)
	_, nestedPhone := nested["driver"].(map[string]any)["phone"]
	require.False(t, nestedPhone)

	driverList = e.call(http.MethodGet, "/v1/trips/"+tripID+"/bookings", nil, authEN(driver.access))
	require.Contains(t, string(driverList.Body), "+99365113002")
	require.NotContains(t, string(driverList.Body), "+99365113001")
	seen := e.call(http.MethodGet, "/v1/trips/"+tripID, nil, authEN(passenger.access))
	require.NotContains(t, string(seen.Body), "+993")

	secondBooking := e.call(http.MethodPost, "/v1/trips/"+tripID+"/bookings", map[string]any{"seats": 1}, authEN(second.access))
	require.Equal(t, http.StatusCreated, secondBooking.Status, string(secondBooking.Body))
	secondApproved := e.call(http.MethodPost, "/v1/bookings/"+secondBooking.str("id")+"/approve", nil, authEN(driver.access))
	require.Equal(t, http.StatusOK, secondApproved.Status, string(secondApproved.Body))
	require.Equal(t, "full", e.queryString(`SELECT status::text FROM trips WHERE id = $1`, tripID))
	require.Equal(t, 0, e.queryInt(`SELECT seats_left FROM trips WHERE id = $1`, tripID))

	shrink := e.call(http.MethodPatch, "/v1/trips/"+tripID, map[string]any{"seats_total": 1}, authEN(driver.access))
	require.Equal(t, http.StatusConflict, shrink.Status)
	require.Equal(t, "INVALID_STATE", shrink.Err.Error.Code)

	third, _ := e.signIn("+99365113004", "passenger-three")
	fullBook := e.call(http.MethodPost, "/v1/trips/"+tripID+"/bookings", map[string]any{"seats": 1}, authEN(third.access))
	require.Equal(t, http.StatusConflict, fullBook.Status)
	require.Equal(t, "SEAT_UNAVAILABLE", fullBook.Err.Error.Code)

	cancelled := e.call(http.MethodPost, "/v1/bookings/"+bookingID+"/cancel", nil, authEN(passenger.access))
	require.Equal(t, http.StatusOK, cancelled.Status, string(cancelled.Body))
	require.Equal(t, "cancelled_by_passenger", cancelled.str("status"))
	require.NotContains(t, string(cancelled.Body), "+993")
	require.Equal(t, "active", e.queryString(`SELECT status::text FROM trips WHERE id = $1`, tripID))
	require.Equal(t, 1, e.queryInt(`SELECT seats_left FROM trips WHERE id = $1`, tripID))

	grow := e.call(http.MethodPatch, "/v1/trips/"+tripID, map[string]any{"seats_total": 3}, authEN(driver.access))
	require.Equal(t, http.StatusOK, grow.Status, string(grow.Body))
	require.Equal(t, "active", grow.str("status"))
	require.Equal(t, float64(2), grow.field("seats_left"))

	early := e.call(http.MethodPost, "/v1/trips/"+tripID+"/complete", nil, authEN(driver.access))
	require.Equal(t, http.StatusConflict, early.Status)
	require.Equal(t, "INVALID_STATE", early.Err.Error.Code)
	e.exec(`UPDATE trips SET departs_at = now() - interval '1 minute' WHERE id = $1`, tripID)
	done := e.call(http.MethodPost, "/v1/trips/"+tripID+"/complete", nil, authEN(driver.access))
	require.Equal(t, http.StatusOK, done.Status, string(done.Body))
	require.Equal(t, "completed", done.str("status"))
	require.Equal(t, "approved", e.queryString(`SELECT status::text FROM bookings WHERE id = $1`, secondBooking.str("id")))
}

func TestTripCancelCascadesBookings(t *testing.T) {
	e := newEnv(t)
	driver, _ := e.signIn("+99365114001", "driver-cancel")
	first, _ := e.signIn("+99365114002", "pax-cancel-1")
	second, _ := e.signIn("+99365114003", "pax-cancel-2")
	tripID := e.createTrip(driver.access, locAshgabat, locMaryCity, 3, 36*time.Hour)

	approved := e.call(http.MethodPost, "/v1/trips/"+tripID+"/bookings", map[string]any{"seats": 1}, authEN(first.access))
	require.Equal(t, http.StatusCreated, approved.Status, string(approved.Body))
	ok := e.call(http.MethodPost, "/v1/bookings/"+approved.str("id")+"/approve", nil, authEN(driver.access))
	require.Equal(t, http.StatusOK, ok.Status, string(ok.Body))
	pending := e.call(http.MethodPost, "/v1/trips/"+tripID+"/bookings", map[string]any{"seats": 2}, authEN(second.access))
	require.Equal(t, http.StatusCreated, pending.Status, string(pending.Body))

	forbidden := e.call(http.MethodPost, "/v1/trips/"+tripID+"/cancel", nil, authEN(first.access))
	require.Equal(t, http.StatusForbidden, forbidden.Status)

	cancelled := e.call(http.MethodPost, "/v1/trips/"+tripID+"/cancel", nil, authEN(driver.access))
	require.Equal(t, http.StatusOK, cancelled.Status, string(cancelled.Body))
	require.Equal(t, "cancelled", cancelled.str("status"))
	require.Equal(t, "cancelled_by_driver", e.queryString(`SELECT status::text FROM bookings WHERE id = $1`, approved.str("id")))
	require.Equal(t, "cancelled_by_driver", e.queryString(`SELECT status::text FROM bookings WHERE id = $1`, pending.str("id")))

	again := e.call(http.MethodPost, "/v1/trips/"+tripID+"/cancel", nil, authEN(driver.access))
	require.Equal(t, http.StatusOK, again.Status, string(again.Body))
	require.Equal(t, "cancelled", again.str("status"))
}

func TestAccountDeletionCascadesTrips(t *testing.T) {
	e := newEnv(t)
	driver, _ := e.signIn("+99365115001", "driver-delete")
	leaving, _ := e.signIn("+99365115002", "pax-delete")
	staying, _ := e.signIn("+99365115003", "pax-stay")

	vehicle := e.call(http.MethodPost, "/v1/me/vehicles", map[string]any{
		"brand": "Toyota", "model": "Camry", "color": "ak", "plate": "AG 1501 AB",
	}, authEN(driver.access))
	require.Equal(t, http.StatusCreated, vehicle.Status, string(vehicle.Body))

	tripID := e.createTrip(driver.access, locAshgabat, locMaryCity, 2, 24*time.Hour)
	e.call(http.MethodPost, "/v1/me/vehicles", map[string]any{
		"brand": "Lada", "model": "2107", "color": "ak", "plate": "AG 1502 AB",
	}, authEN(leaving.access))

	left := e.call(http.MethodPost, "/v1/trips/"+tripID+"/bookings", map[string]any{"seats": 1}, authEN(leaving.access))
	require.Equal(t, http.StatusCreated, left.Status, string(left.Body))
	stay := e.call(http.MethodPost, "/v1/trips/"+tripID+"/bookings", map[string]any{"seats": 1}, authEN(staying.access))
	require.Equal(t, http.StatusCreated, stay.Status, string(stay.Body))
	require.Equal(t, http.StatusOK, e.call(http.MethodPost, "/v1/bookings/"+left.str("id")+"/approve", nil, authEN(driver.access)).Status)
	require.Equal(t, http.StatusOK, e.call(http.MethodPost, "/v1/bookings/"+stay.str("id")+"/approve", nil, authEN(driver.access)).Status)
	require.Equal(t, "full", e.queryString(`SELECT status::text FROM trips WHERE id = $1`, tripID))

	deleted := e.call(http.MethodDelete, "/v1/me", nil, authEN(leaving.access))
	require.Equal(t, http.StatusNoContent, deleted.Status, string(deleted.Body))
	require.Equal(t, "cancelled_by_passenger", e.queryString(`SELECT status::text FROM bookings WHERE id = $1`, left.str("id")))
	require.Equal(t, "approved", e.queryString(`SELECT status::text FROM bookings WHERE id = $1`, stay.str("id")))
	require.Equal(t, "active", e.queryString(`SELECT status::text FROM trips WHERE id = $1`, tripID))
	require.Equal(t, 1, e.queryInt(`SELECT seats_left FROM trips WHERE id = $1`, tripID))
	require.Equal(t, 0, e.queryInt(`SELECT count(*) FROM vehicles WHERE user_id = $1`, leaving.userID))
	require.Equal(t, 1, e.queryInt(`SELECT count(*) FROM vehicles WHERE user_id = $1`, driver.userID))

	driverGone := e.call(http.MethodDelete, "/v1/me", nil, authEN(driver.access))
	require.Equal(t, http.StatusNoContent, driverGone.Status, string(driverGone.Body))
	require.Equal(t, "cancelled", e.queryString(`SELECT status::text FROM trips WHERE id = $1`, tripID))
	require.Equal(t, "cancelled_by_driver", e.queryString(`SELECT status::text FROM bookings WHERE id = $1`, stay.str("id")))
	require.Equal(t, 0, e.queryInt(`SELECT count(*) FROM vehicles WHERE user_id = $1`, driver.userID))
}

func TestPastTripsCompletedOrExpired(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	driver, _ := e.signIn("+99365116001", "driver-job")
	passenger, _ := e.signIn("+99365116002", "pax-job")

	consistent := e.createTrip(driver.access, locAshgabat, locMaryCity, 2, 24*time.Hour)
	booked := e.call(http.MethodPost, "/v1/trips/"+consistent+"/bookings", map[string]any{"seats": 1}, authEN(passenger.access))
	require.Equal(t, http.StatusCreated, booked.Status, string(booked.Body))
	require.Equal(t, http.StatusOK, e.call(http.MethodPost, "/v1/bookings/"+booked.str("id")+"/approve", nil, authEN(driver.access)).Status)
	e.exec(`UPDATE trips SET departs_at = now() - interval '2 hours' WHERE id = $1`, consistent)

	empty := e.createTrip(driver.access, locAshgabat, locBayramaly, 2, 30*time.Hour)
	e.exec(`UPDATE trips SET departs_at = now() - interval '3 hours' WHERE id = $1`, empty)

	pendingOnly := e.createTrip(driver.access, locAnau, locAshgabat, 2, 32*time.Hour)
	pending := e.call(http.MethodPost, "/v1/trips/"+pendingOnly+"/bookings", map[string]any{"seats": 1}, authEN(passenger.access))
	require.Equal(t, http.StatusCreated, pending.Status, string(pending.Body))
	e.exec(`UPDATE trips SET departs_at = now() - interval '4 hours' WHERE id = $1`, pendingOnly)

	future := e.createTrip(driver.access, locAshgabat, locMaryWelayat, 2, 48*time.Hour)

	e.exec(`
		INSERT INTO trips (driver_id, from_location_id, to_location_id, departs_at, seats_total, seats_left, status, note)
		VALUES ($1, $2, $3, now() - interval '5 hours', 2, 2, 'active', 'inconsistent')
	`, driver.userID, locAshgabat, locMaryCity)
	inconsistent := e.queryString(`SELECT id::text FROM trips WHERE note = 'inconsistent'`)
	e.exec(`INSERT INTO bookings (trip_id, passenger_id, seats, status) VALUES ($1, $2, 1, 'approved')`, inconsistent, passenger.userID)

	stats, err := e.jobs.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.CompletedTrips)
	require.Equal(t, int64(2), stats.ExpiredTrips)
	require.Equal(t, int64(1), stats.InconsistentTrips)

	require.Equal(t, "completed", e.queryString(`SELECT status::text FROM trips WHERE id = $1`, consistent))
	require.Equal(t, "approved", e.queryString(`SELECT status::text FROM bookings WHERE trip_id = $1`, consistent))
	require.Equal(t, "expired", e.queryString(`SELECT status::text FROM trips WHERE id = $1`, empty))
	require.Equal(t, "expired", e.queryString(`SELECT status::text FROM trips WHERE id = $1`, pendingOnly))
	require.Equal(t, "pending", e.queryString(`SELECT status::text FROM bookings WHERE id = $1`, pending.str("id")))
	require.Equal(t, "active", e.queryString(`SELECT status::text FROM trips WHERE id = $1`, future))
	require.Equal(t, "active", e.queryString(`SELECT status::text FROM trips WHERE id = $1`, inconsistent))
	require.Equal(t, 2, e.queryInt(`SELECT seats_left FROM trips WHERE id = $1`, inconsistent))
}

func TestApproveDoesNotOversell(t *testing.T) {
	e := newEnv(t)
	const seats = 3
	const n = 8
	driver, _ := e.signIn("+99365117001", "driver-race")
	tripID := e.createTrip(driver.access, locAshgabat, locMaryCity, seats, 24*time.Hour)

	ids := make([]string, n)
	for i := 0; i < n; i++ {
		pax, _ := e.signIn(fmt.Sprintf("+99367%06d", 100+i), "pax-race")
		res := e.call(http.MethodPost, "/v1/trips/"+tripID+"/bookings", map[string]any{"seats": 1}, authEN(pax.access))
		require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
		ids[i] = res.str("id")
	}

	var wg sync.WaitGroup
	codes := make([]int, n)
	bodies := make([][]byte, n)
	wg.Add(n)
	for i := range ids {
		go func(i int) {
			defer wg.Done()
			codes[i], bodies[i], _ = e.do(http.MethodPost, "/v1/bookings/"+ids[i]+"/approve", nil, authEN(driver.access))
		}(i)
	}
	wg.Wait()

	var ok, conflict int
	for i, code := range codes {
		switch code {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
			require.Contains(t, string(bodies[i]), "SEAT_UNAVAILABLE")
		default:
			t.Fatalf("approve %d status %d body %s", i, code, bodies[i])
		}
	}
	require.Equal(t, seats, ok)
	require.Equal(t, n-seats, conflict)

	require.Equal(t, 0, e.queryInt(`SELECT seats_left FROM trips WHERE id = $1`, tripID))
	require.GreaterOrEqual(t, e.queryInt(`SELECT seats_left FROM trips WHERE id = $1`, tripID), 0)
	require.Equal(t, seats, e.queryInt(`SELECT seats_total FROM trips WHERE id = $1`, tripID))
	require.Equal(t, "full", e.queryString(`SELECT status::text FROM trips WHERE id = $1`, tripID))
	approvedSeats := e.queryInt(`SELECT COALESCE(SUM(seats), 0) FROM bookings WHERE trip_id = $1 AND status = 'approved'`, tripID)
	require.Equal(t, seats, approvedSeats)
	require.LessOrEqual(t, approvedSeats, seats)
	require.Equal(t, seats, e.queryInt(`SELECT count(*) FROM bookings WHERE trip_id = $1 AND status = 'approved'`, tripID))
}

func (e *env) createTrip(token, from, to string, seats int, departIn time.Duration) string {
	e.t.Helper()
	res := e.call(http.MethodPost, "/v1/trips", map[string]any{
		"from_location_id": from,
		"to_location_id":   to,
		"departs_at":       time.Now().Add(departIn).UTC().Format(time.RFC3339),
		"seats_total":      seats,
		"suggested_price":  "25.50",
		"currency":         "TMT",
		"pickup_note":      "bazaar",
	}, authEN(token))
	require.Equal(e.t, http.StatusCreated, res.Status, string(res.Body))
	require.NotEmpty(e.t, res.str("id"))
	require.NotContains(e.t, string(res.Body), `"phone"`)
	return res.str("id")
}

func (e *env) do(method, path string, body any, headers map[string]string) (int, []byte, error) {
	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, reader)
	if err != nil {
		return 0, nil, err
	}
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := e.srv.Client().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	return res.StatusCode, raw, err
}

func authEN(token string) map[string]string {
	return map[string]string{
		"Authorization":   "Bearer " + token,
		"Accept-Language": "en",
	}
}

func tripIDs(t *testing.T, res response) []string {
	t.Helper()
	raw, _ := res.decoded["trips"].([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		out = append(out, item.(map[string]any)["id"].(string))
	}
	return out
}
