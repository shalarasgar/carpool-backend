package integration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/notify"
	"github.com/shalarasgar/carpool-backend/internal/service"
	"github.com/shalarasgar/carpool-backend/internal/store"
	"github.com/stretchr/testify/require"
)

func TestReviews(t *testing.T) {
	e := newEnv(t)
	driver, _ := e.signIn("+99365118001", "rev-driver")
	passenger, _ := e.signIn("+99365118002", "rev-pax")
	other, _ := e.signIn("+99365118003", "rev-other")
	second, _ := e.signIn("+99365118004", "rev-second")
	e.call(http.MethodPatch, "/v1/me", map[string]any{"name": "Driver"}, bearer(driver.access))
	e.call(http.MethodPatch, "/v1/me", map[string]any{"name": "Passenger"}, bearer(passenger.access))

	tripID := e.completedPair(driver, passenger)
	secondTrip := e.createTrip(driver.access, locAshgabat, locBayramaly, 2, 36*time.Hour)
	for _, pax := range []session{passenger, second} {
		booked := e.call(http.MethodPost, "/v1/trips/"+secondTrip+"/bookings", map[string]any{"seats": 1}, authEN(pax.access))
		require.Equal(t, http.StatusCreated, booked.Status, string(booked.Body))
		require.Equal(t, http.StatusOK, e.call(http.MethodPost, "/v1/bookings/"+booked.str("id")+"/approve", nil, authEN(driver.access)).Status)
	}
	e.completeTrip(driver.access, secondTrip)

	openTrip := e.createTrip(driver.access, locAshgabat, locMaryWelayat, 2, 40*time.Hour)
	openBooked := e.call(http.MethodPost, "/v1/trips/"+openTrip+"/bookings", map[string]any{"seats": 1}, authEN(passenger.access))
	require.Equal(t, http.StatusCreated, openBooked.Status, string(openBooked.Body))
	require.Equal(t, http.StatusOK, e.call(http.MethodPost, "/v1/bookings/"+openBooked.str("id")+"/approve", nil, authEN(driver.access)).Status)

	early := e.call(http.MethodPost, "/v1/trips/"+openTrip+"/reviews", map[string]any{
		"to_user_id": passenger.userID, "rating": 5,
	}, authEN(driver.access))
	require.Equal(t, http.StatusConflict, early.Status, string(early.Body))
	require.Equal(t, "INVALID_STATE", early.Err.Error.Code)

	self := e.call(http.MethodPost, "/v1/trips/"+tripID+"/reviews", map[string]any{
		"to_user_id": driver.userID, "rating": 5,
	}, authEN(driver.access))
	require.Equal(t, http.StatusForbidden, self.Status)
	require.Equal(t, "FORBIDDEN", self.Err.Error.Code)

	stranger := e.call(http.MethodPost, "/v1/trips/"+tripID+"/reviews", map[string]any{
		"to_user_id": driver.userID, "rating": 4,
	}, authEN(other.access))
	require.Equal(t, http.StatusForbidden, stranger.Status)
	require.Equal(t, "FORBIDDEN", stranger.Err.Error.Code)

	betweenPassengers := e.call(http.MethodPost, "/v1/trips/"+secondTrip+"/reviews", map[string]any{
		"to_user_id": passenger.userID, "rating": 3,
	}, authEN(second.access))
	require.Equal(t, http.StatusForbidden, betweenPassengers.Status)
	require.Equal(t, "FORBIDDEN", betweenPassengers.Err.Error.Code)

	created := e.call(http.MethodPost, "/v1/trips/"+tripID+"/reviews", map[string]any{
		"to_user_id": driver.userID, "rating": 5, "comment": "on time",
	}, authEN(passenger.access))
	require.Equal(t, http.StatusCreated, created.Status, string(created.Body))
	require.Equal(t, "Passenger", created.decoded["from_user"].(map[string]any)["name"])
	require.NotContains(t, string(created.Body), "+993")
	require.NotContains(t, string(created.Body), "phone")

	again := e.call(http.MethodPost, "/v1/trips/"+tripID+"/reviews", map[string]any{
		"to_user_id": driver.userID, "rating": 1,
	}, authEN(passenger.access))
	require.Equal(t, http.StatusConflict, again.Status)
	require.Equal(t, "REVIEW_EXISTS", again.Err.Error.Code)
	require.Equal(t, 1, e.queryInt(`SELECT rating_count FROM users WHERE id = $1`, driver.userID))

	back := e.call(http.MethodPost, "/v1/trips/"+tripID+"/reviews", map[string]any{
		"to_user_id": passenger.userID, "rating": 4, "comment": "polite",
	}, authEN(driver.access))
	require.Equal(t, http.StatusCreated, back.Status, string(back.Body))

	listed := e.call(http.MethodGet, "/v1/users/"+driver.userID+"/reviews", nil, authEN(other.access))
	require.Equal(t, http.StatusOK, listed.Status, string(listed.Body))
	require.Len(t, listed.decoded["reviews"].([]any), 1)
	require.NotContains(t, string(listed.Body), "+993")
	require.Equal(t, float64(5), listed.decoded["reviews"].([]any)[0].(map[string]any)["rating"])

	stale := e.createTrip(driver.access, locAnau, locAshgabat, 1, 20*time.Hour)
	staleBooked := e.call(http.MethodPost, "/v1/trips/"+stale+"/bookings", map[string]any{"seats": 1}, authEN(passenger.access))
	require.Equal(t, http.StatusCreated, staleBooked.Status, string(staleBooked.Body))
	require.Equal(t, http.StatusOK, e.call(http.MethodPost, "/v1/bookings/"+staleBooked.str("id")+"/approve", nil, authEN(driver.access)).Status)
	e.exec(`UPDATE trips SET status = 'completed', departs_at = now() - interval '15 days' WHERE id = $1`, stale)
	closed := e.call(http.MethodPost, "/v1/trips/"+stale+"/reviews", map[string]any{
		"to_user_id": driver.userID, "rating": 5,
	}, authEN(passenger.access))
	require.Equal(t, http.StatusConflict, closed.Status, string(closed.Body))
	require.Equal(t, "REVIEW_WINDOW_CLOSED", closed.Err.Error.Code)
}

func TestReviewRatingUnderParallelReviews(t *testing.T) {
	e := newEnv(t)
	const n = 5
	ratings := []int{5, 4, 4, 3, 2}
	driver, _ := e.signIn("+99365118100", "agg-driver")
	tripID := e.createTrip(driver.access, locAshgabat, locMaryCity, n, 24*time.Hour)
	passengers := make([]session, n)
	for i := 0; i < n; i++ {
		passengers[i], _ = e.signIn(fmt.Sprintf("+99365118%03d", 110+i), "agg-pax")
		res := e.call(http.MethodPost, "/v1/trips/"+tripID+"/bookings", map[string]any{"seats": 1}, authEN(passengers[i].access))
		require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
		require.Equal(t, http.StatusOK, e.call(http.MethodPost, "/v1/bookings/"+res.str("id")+"/approve", nil, authEN(driver.access)).Status)
	}
	e.completeTrip(driver.access, tripID)

	var wg sync.WaitGroup
	codes := make([]int, n)
	bodies := make([][]byte, n)
	wg.Add(n)
	for i := range passengers {
		go func(i int) {
			defer wg.Done()
			codes[i], bodies[i], _ = e.do(http.MethodPost, "/v1/trips/"+tripID+"/reviews", map[string]any{
				"to_user_id": driver.userID,
				"rating":     ratings[i],
			}, authEN(passengers[i].access))
		}(i)
	}
	wg.Wait()
	for i, code := range codes {
		require.Equal(t, http.StatusCreated, code, "review %d: %s", i, bodies[i])
	}
	require.Equal(t, n, e.queryInt(`SELECT rating_count FROM users WHERE id = $1`, driver.userID))
	require.Equal(t, "3.60", e.queryString(`SELECT rating_avg::text FROM users WHERE id = $1`, driver.userID))
	require.Equal(t, n, e.queryInt(`SELECT count(*) FROM reviews WHERE to_user_id = $1`, driver.userID))
}

func TestBlocksHideSearchAndRejectNewBookings(t *testing.T) {
	e := newEnv(t)
	driver, _ := e.signIn("+99365118201", "block-driver")
	passenger, _ := e.signIn("+99365118202", "block-pax")
	e.call(http.MethodPatch, "/v1/me", map[string]any{"name": "Blocked Driver"}, bearer(driver.access))

	kept := e.createTrip(driver.access, locAshgabat, locMaryCity, 2, 30*time.Hour)
	booked := e.call(http.MethodPost, "/v1/trips/"+kept+"/bookings", map[string]any{"seats": 1}, authEN(passenger.access))
	require.Equal(t, http.StatusCreated, booked.Status, string(booked.Body))
	approved := e.call(http.MethodPost, "/v1/bookings/"+booked.str("id")+"/approve", nil, authEN(driver.access))
	require.Equal(t, http.StatusOK, approved.Status, string(approved.Body))

	other := e.createTrip(driver.access, locAshgabat, locBayramaly, 2, 40*time.Hour)
	theirs := e.createTrip(passenger.access, locAnau, locAshgabat, 2, 28*time.Hour)

	seen := e.call(http.MethodGet, "/v1/trips?from="+locAshgabat+"&to="+locBayramaly, nil, authEN(passenger.access))
	require.Equal(t, []string{other}, tripIDs(t, seen))
	seen = e.call(http.MethodGet, "/v1/trips?from="+locAnau, nil, authEN(driver.access))
	require.Equal(t, []string{theirs}, tripIDs(t, seen))

	self := e.call(http.MethodPost, "/v1/users/"+passenger.userID+"/block", nil, authEN(passenger.access))
	require.Equal(t, http.StatusForbidden, self.Status)

	blocked := e.call(http.MethodPost, "/v1/users/"+driver.userID+"/block", nil, authEN(passenger.access))
	require.Equal(t, http.StatusCreated, blocked.Status, string(blocked.Body))
	require.Equal(t, driver.userID, blocked.str("user_id"))
	require.NotContains(t, string(blocked.Body), "+993")
	again := e.call(http.MethodPost, "/v1/users/"+driver.userID+"/block", nil, authEN(passenger.access))
	require.Equal(t, http.StatusOK, again.Status, string(again.Body))

	hidden := e.call(http.MethodGet, "/v1/trips?from="+locAshgabat+"&to="+locBayramaly, nil, authEN(passenger.access))
	require.Empty(t, tripIDs(t, hidden))
	reverse := e.call(http.MethodGet, "/v1/trips?from="+locAnau, nil, authEN(driver.access))
	require.Empty(t, tripIDs(t, reverse))

	denied := e.call(http.MethodPost, "/v1/trips/"+other+"/bookings", map[string]any{"seats": 1}, authEN(passenger.access))
	require.Equal(t, http.StatusForbidden, denied.Status)
	require.Equal(t, "FORBIDDEN", denied.Err.Error.Code)
	require.Equal(t, "approved", e.queryString(`SELECT status::text FROM bookings WHERE id = $1`, booked.str("id")))
	require.Equal(t, 1, e.queryInt(`SELECT seats_left FROM trips WHERE id = $1`, kept))

	list := e.call(http.MethodGet, "/v1/me/blocks", nil, authEN(passenger.access))
	require.Equal(t, http.StatusOK, list.Status, string(list.Body))
	require.NotContains(t, string(list.Body), "+993")
	require.Equal(t, "Blocked Driver", list.decoded["blocks"].([]any)[0].(map[string]any)["name"])

	gone := e.call(http.MethodDelete, "/v1/users/"+driver.userID+"/block", nil, authEN(passenger.access))
	require.Equal(t, http.StatusNoContent, gone.Status)
	againDelete := e.call(http.MethodDelete, "/v1/users/"+driver.userID+"/block", nil, authEN(passenger.access))
	require.Equal(t, http.StatusNoContent, againDelete.Status)
}

func TestNotifications(t *testing.T) {
	e := newEnv(t)
	driver, _ := e.signIn("+99365118301", "note-driver")
	first, _ := e.signIn("+99365118302", "note-ru")
	second, _ := e.signIn("+99365118303", "note-tk")
	third, _ := e.signIn("+99365118304", "note-en")
	require.Equal(t, http.StatusOK, e.call(http.MethodPatch, "/v1/me", map[string]any{"locale": "en"}, bearer(driver.access)).Status)
	require.Equal(t, http.StatusOK, e.call(http.MethodPatch, "/v1/me", map[string]any{"locale": "ru"}, bearer(first.access)).Status)
	require.Equal(t, http.StatusOK, e.call(http.MethodPatch, "/v1/me", map[string]any{"locale": "tk"}, bearer(second.access)).Status)

	tripID := e.createTrip(driver.access, locAshgabat, locMaryCity, 3, 48*time.Hour)
	bookings := make([]string, 0, 3)
	for _, pax := range []session{first, second, third} {
		res := e.call(http.MethodPost, "/v1/trips/"+tripID+"/bookings", map[string]any{"seats": 1}, authEN(pax.access))
		require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
		bookings = append(bookings, res.str("id"))
	}

	headers := authEN(driver.access)
	headers["Accept-Language"] = "tk"
	var cursor string
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		path := "/v1/me/notifications?limit=1"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		page := e.call(http.MethodGet, path, nil, headers)
		require.Equal(t, http.StatusOK, page.Status, string(page.Body))
		require.Equal(t, float64(3), page.field("unread_count"))
		items := page.decoded["notifications"].([]any)
		require.Len(t, items, 1)
		item := items[0].(map[string]any)
		require.Equal(t, "booking_requested", item["type"])
		require.Equal(t, "New seat request", item["title"])
		require.NotContains(t, string(page.Body), "+993")
		require.NotContains(t, string(page.Body), "phone")
		seen[item["id"].(string)] = true
		if i < 2 {
			cursor, _ = page.decoded["next_cursor"].(string)
			require.NotEmpty(t, cursor)
		}
	}
	require.Len(t, seen, 3)

	oneID := ""
	for id := range seen {
		oneID = id
		break
	}
	marked := e.call(http.MethodPost, "/v1/me/notifications/read", map[string]any{"ids": []string{oneID}}, authEN(driver.access))
	require.Equal(t, http.StatusOK, marked.Status, string(marked.Body))
	require.Equal(t, float64(2), marked.field("unread_count"))
	all := e.call(http.MethodPost, "/v1/me/notifications/read", map[string]any{"all": true}, authEN(driver.access))
	require.Equal(t, http.StatusOK, all.Status, string(all.Body))
	require.Equal(t, float64(0), all.field("unread_count"))

	require.Equal(t, http.StatusOK, e.call(http.MethodPost, "/v1/bookings/"+bookings[0]+"/approve", nil, authEN(driver.access)).Status)
	ru := e.call(http.MethodGet, "/v1/me/notifications", nil, authEN(first.access))
	require.Equal(t, http.StatusOK, ru.Status, string(ru.Body))
	require.Equal(t, "Запрос подтверждён", ru.decoded["notifications"].([]any)[0].(map[string]any)["title"])
	require.NotContains(t, string(ru.Body), "+993")

	require.Equal(t, http.StatusOK, e.call(http.MethodPost, "/v1/bookings/"+bookings[1]+"/reject", nil, authEN(driver.access)).Status)
	tk := e.call(http.MethodGet, "/v1/me/notifications", nil, authEN(second.access))
	require.Equal(t, "Haýyş ret edildi", tk.decoded["notifications"].([]any)[0].(map[string]any)["title"])

	require.Equal(t, http.StatusOK, e.call(http.MethodPost, "/v1/bookings/"+bookings[0]+"/cancel", nil, authEN(first.access)).Status)
	driverInbox := e.call(http.MethodGet, "/v1/me/notifications?limit=1", nil, authEN(driver.access))
	require.Equal(t, "booking_cancelled", driverInbox.decoded["notifications"].([]any)[0].(map[string]any)["type"])
	require.Equal(t, "Booking cancelled", driverInbox.decoded["notifications"].([]any)[0].(map[string]any)["title"])

	otherTrip := e.createTrip(driver.access, locAshgabat, locBayramaly, 1, 50*time.Hour)
	otherBooked := e.call(http.MethodPost, "/v1/trips/"+otherTrip+"/bookings", map[string]any{"seats": 1}, authEN(first.access))
	require.Equal(t, http.StatusCreated, otherBooked.Status, string(otherBooked.Body))
	require.Equal(t, http.StatusOK, e.call(http.MethodPost, "/v1/bookings/"+otherBooked.str("id")+"/approve", nil, authEN(driver.access)).Status)
	require.Equal(t, http.StatusOK, e.call(http.MethodPost, "/v1/trips/"+otherTrip+"/cancel", nil, authEN(driver.access)).Status)
	cancelled := e.call(http.MethodGet, "/v1/me/notifications?limit=1", nil, authEN(first.access))
	require.Equal(t, "trip_cancelled", cancelled.decoded["notifications"].([]any)[0].(map[string]any)["type"])
	require.Equal(t, "Поездка отменена", cancelled.decoded["notifications"].([]any)[0].(map[string]any)["title"])

	before := e.queryInt(`SELECT count(*) FROM notifications WHERE user_id = $1`, driver.userID)
	err := store.RunInTx(context.Background(), e.pool, func(q *store.Queries) error {
		if err := (notify.Inbox{}).Notify(store.WithQueries(context.Background(), q), notify.Event{
			Kind:        notify.KindBookingRequested,
			RecipientID: driver.userID,
			ActorID:     first.userID,
			TripID:      tripID,
			BookingID:   bookings[2],
		}); err != nil {
			return err
		}
		return errors.New("fail the transaction")
	})
	require.Error(t, err)
	require.Equal(t, before, e.queryInt(`SELECT count(*) FROM notifications WHERE user_id = $1`, driver.userID))

	rollbackTrip := e.createTrip(driver.access, locAnau, locAshgabat, 1, 60*time.Hour)
	broken := service.NewBookings(e.pool, failNotifier{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err = broken.Create(context.Background(), third.userID, rollbackTrip, 1)
	require.Error(t, err)
	require.Equal(t, 0, e.queryInt(`SELECT count(*) FROM bookings WHERE trip_id = $1`, rollbackTrip))
	require.Equal(t, before, e.queryInt(`SELECT count(*) FROM notifications WHERE user_id = $1`, driver.userID))
}

func TestAdminReportsBlockLocationAndStats(t *testing.T) {
	e := newEnv(t)
	noAuth := e.call(http.MethodGet, "/admin/v1/reports", nil, nil)
	require.Equal(t, http.StatusForbidden, noAuth.Status)
	wrong := e.call(http.MethodGet, "/admin/v1/sms/stats", nil, bearer("not-the-admin-token"))
	require.Equal(t, http.StatusForbidden, wrong.Status)

	reporter, _ := e.signIn("+99365118401", "report-user")
	target, _ := e.signIn("+99365118402", "report-target")
	userToken := e.call(http.MethodGet, "/admin/v1/gateways", nil, bearer(reporter.access))
	require.Equal(t, http.StatusForbidden, userToken.Status)

	self := e.call(http.MethodPost, "/v1/reports", map[string]any{
		"reported_user_id": reporter.userID, "reason": "spam",
	}, authEN(reporter.access))
	require.Equal(t, http.StatusForbidden, self.Status)

	bad := e.call(http.MethodPost, "/v1/reports", map[string]any{
		"reported_user_id": target.userID, "reason": "boring",
	}, authEN(reporter.access))
	require.Equal(t, http.StatusUnprocessableEntity, bad.Status)

	first := e.call(http.MethodPost, "/v1/reports", map[string]any{
		"reported_user_id": target.userID, "reason": "scam", "details": "asked for money",
	}, authEN(reporter.access))
	require.Equal(t, http.StatusCreated, first.Status, string(first.Body))
	require.Equal(t, "open", first.str("status"))
	require.NotContains(t, string(first.Body), "+993")
	second := e.call(http.MethodPost, "/v1/reports", map[string]any{
		"reported_user_id": target.userID, "reason": "no_show",
	}, authEN(reporter.access))
	require.Equal(t, http.StatusCreated, second.Status, string(second.Body))

	listed := e.call(http.MethodGet, "/admin/v1/reports?status=open&page_size=1", nil, bearer(testAdminToken))
	require.Equal(t, http.StatusOK, listed.Status, string(listed.Body))
	require.Equal(t, true, listed.field("has_more"))
	require.Len(t, listed.decoded["reports"].([]any), 1)

	updated := e.call(http.MethodPatch, "/admin/v1/reports/"+first.str("id"), map[string]any{"status": "resolved"}, bearer(testAdminToken))
	require.Equal(t, http.StatusOK, updated.Status, string(updated.Body))
	require.Equal(t, "resolved", updated.str("status"))
	open := e.call(http.MethodGet, "/admin/v1/reports?status=open", nil, bearer(testAdminToken))
	require.Len(t, open.decoded["reports"].([]any), 1)
	resolved := e.call(http.MethodGet, "/admin/v1/reports?status=resolved", nil, bearer(testAdminToken))
	require.Equal(t, first.str("id"), resolved.decoded["reports"].([]any)[0].(map[string]any)["id"])

	for i := 0; i < 3; i++ {
		res := e.call(http.MethodPost, "/v1/reports", map[string]any{
			"reported_user_id": target.userID, "reason": "spam",
		}, authEN(reporter.access))
		require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	}
	limited := e.call(http.MethodPost, "/v1/reports", map[string]any{
		"reported_user_id": target.userID, "reason": "spam",
	}, authEN(reporter.access))
	require.Equal(t, http.StatusTooManyRequests, limited.Status)
	require.Equal(t, "RATE_LIMITED", limited.Err.Error.Code)
	require.NotEmpty(t, limited.Header.Get("Retry-After"))

	driver, _ := e.signIn("+99365118403", "admin-driver")
	passenger, _ := e.signIn("+99365118404", "admin-pax")
	require.Equal(t, http.StatusOK, e.call(http.MethodPatch, "/v1/me", map[string]any{"name": "Still Named"}, bearer(driver.access)).Status)
	vehicle := e.call(http.MethodPost, "/v1/me/vehicles", map[string]any{
		"brand": "Toyota", "model": "Camry", "color": "ak", "plate": "AG 1840 AB",
	}, bearer(driver.access))
	require.Equal(t, http.StatusCreated, vehicle.Status, string(vehicle.Body))
	own := e.createTrip(driver.access, locAshgabat, locMaryCity, 2, 24*time.Hour)
	ownBooked := e.call(http.MethodPost, "/v1/trips/"+own+"/bookings", map[string]any{"seats": 1}, authEN(passenger.access))
	require.Equal(t, http.StatusCreated, ownBooked.Status, string(ownBooked.Body))
	require.Equal(t, http.StatusOK, e.call(http.MethodPost, "/v1/bookings/"+ownBooked.str("id")+"/approve", nil, authEN(driver.access)).Status)
	other := e.createTrip(passenger.access, locAnau, locAshgabat, 2, 26*time.Hour)
	otherBooked := e.call(http.MethodPost, "/v1/trips/"+other+"/bookings", map[string]any{"seats": 1}, authEN(driver.access))
	require.Equal(t, http.StatusCreated, otherBooked.Status, string(otherBooked.Body))

	blocked := e.call(http.MethodPost, "/admin/v1/users/"+driver.userID+"/block", nil, bearer(testAdminToken))
	require.Equal(t, http.StatusOK, blocked.Status, string(blocked.Body))
	require.Equal(t, true, blocked.field("is_blocked"))
	require.Equal(t, "+99365118403", e.queryString(`SELECT phone FROM users WHERE id = $1`, driver.userID))
	require.Equal(t, "Still Named", e.queryString(`SELECT name FROM users WHERE id = $1`, driver.userID))
	require.Equal(t, 0, e.queryInt(`SELECT count(*) FROM vehicles WHERE user_id = $1`, driver.userID))
	require.Equal(t, "cancelled", e.queryString(`SELECT status::text FROM trips WHERE id = $1`, own))
	require.Equal(t, "cancelled_by_driver", e.queryString(`SELECT status::text FROM bookings WHERE id = $1`, ownBooked.str("id")))
	require.Equal(t, "cancelled_by_passenger", e.queryString(`SELECT status::text FROM bookings WHERE id = $1`, otherBooked.str("id")))
	require.Equal(t, "active", e.queryString(`SELECT status::text FROM trips WHERE id = $1`, other))
	me := e.call(http.MethodGet, "/v1/me", nil, bearer(driver.access))
	require.Equal(t, http.StatusForbidden, me.Status)
	require.Equal(t, "USER_BLOCKED", me.Err.Error.Code)
	refresh := e.call(http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": driver.refresh}, nil)
	require.Equal(t, http.StatusUnauthorized, refresh.Status)

	unblocked := e.call(http.MethodDelete, "/admin/v1/users/"+driver.userID+"/block", nil, bearer(testAdminToken))
	require.Equal(t, http.StatusOK, unblocked.Status, string(unblocked.Body))
	require.Equal(t, false, unblocked.field("is_blocked"))
	me = e.call(http.MethodGet, "/v1/me", nil, bearer(driver.access))
	require.Equal(t, http.StatusOK, me.Status, string(me.Body))
	require.Equal(t, "cancelled", e.queryString(`SELECT status::text FROM trips WHERE id = $1`, own))
	refresh = e.call(http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": driver.refresh}, nil)
	require.Equal(t, http.StatusUnauthorized, refresh.Status)

	place := e.call(http.MethodPatch, "/admin/v1/locations/"+locMaryCity, map[string]any{"is_active": false}, bearer(testAdminToken))
	require.Equal(t, http.StatusOK, place.Status, string(place.Body))
	require.Equal(t, false, place.field("is_active"))
	require.Equal(t, "cancelled", e.queryString(`SELECT status::text FROM trips WHERE id = $1`, own))
	live := e.createTrip(passenger.access, locAshgabat, locBayramaly, 1, 30*time.Hour)
	got := e.call(http.MethodGet, "/v1/trips/"+live, nil, authEN(passenger.access))
	require.Equal(t, http.StatusOK, got.Status, string(got.Body))
	e.call(http.MethodPatch, "/admin/v1/locations/"+locBayramaly, map[string]any{"is_active": false}, bearer(testAdminToken))
	got = e.call(http.MethodGet, "/v1/trips/"+live, nil, authEN(passenger.access))
	require.Equal(t, http.StatusOK, got.Status, string(got.Body))
	require.Equal(t, "active", got.str("status"))
	rejected := e.call(http.MethodPost, "/v1/trips", map[string]any{
		"from_location_id": locAshgabat,
		"to_location_id":   locBayramaly,
		"departs_at":       time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339),
		"seats_total":      1,
	}, authEN(passenger.access))
	require.Equal(t, http.StatusUnprocessableEntity, rejected.Status, string(rejected.Body))

	_, key := e.createGateway("phase-e-phone")
	stats := e.call(http.MethodGet, "/admin/v1/sms/stats", nil, bearer(testAdminToken))
	require.Equal(t, http.StatusOK, stats.Status, string(stats.Body))
	sum := int(stats.field("pending").(float64) + stats.field("processing").(float64) + stats.field("sent").(float64) + stats.field("failed").(float64))
	require.Equal(t, e.queryInt(`SELECT count(*) FROM sms_queue`), sum)
	require.GreaterOrEqual(t, int(stats.field("pending").(float64)), 1)
	gateways := e.call(http.MethodGet, "/admin/v1/gateways", nil, bearer(testAdminToken))
	require.Equal(t, http.StatusOK, gateways.Status, string(gateways.Body))
	require.NotContains(t, string(gateways.Body), key)
	require.NotContains(t, string(gateways.Body), "api_key")
	found := false
	for _, item := range gateways.decoded["gateways"].([]any) {
		gw := item.(map[string]any)
		if gw["name"] == "phase-e-phone" {
			found = true
			require.Equal(t, false, gw["healthy"])
		}
	}
	require.True(t, found)
	beat := e.call(http.MethodPost, "/gateway/v1/heartbeat", nil, bearer(key))
	require.Equal(t, http.StatusOK, beat.Status, string(beat.Body))
	gateways = e.call(http.MethodGet, "/admin/v1/gateways", nil, bearer(testAdminToken))
	for _, item := range gateways.decoded["gateways"].([]any) {
		gw := item.(map[string]any)
		if gw["name"] == "phase-e-phone" {
			require.Equal(t, true, gw["healthy"])
		}
	}
}

func TestJobCleansStalePendingBookingsAndReadNotifications(t *testing.T) {
	e := newEnv(t)
	driver, _ := e.signIn("+99365118501", "job-driver")
	passenger, _ := e.signIn("+99365118502", "job-pax")

	past := e.createTrip(driver.access, locAshgabat, locMaryCity, 2, 24*time.Hour)
	pastBooked := e.call(http.MethodPost, "/v1/trips/"+past+"/bookings", map[string]any{"seats": 1}, authEN(passenger.access))
	require.Equal(t, http.StatusCreated, pastBooked.Status, string(pastBooked.Body))
	e.exec(`UPDATE trips SET departs_at = now() - interval '2 hours' WHERE id = $1`, past)

	future := e.createTrip(driver.access, locAshgabat, locBayramaly, 2, 48*time.Hour)
	futureBooked := e.call(http.MethodPost, "/v1/trips/"+future+"/bookings", map[string]any{"seats": 1}, authEN(passenger.access))
	require.Equal(t, http.StatusCreated, futureBooked.Status, string(futureBooked.Body))

	e.exec(`INSERT INTO notifications (user_id, type, payload, read_at, created_at)
		VALUES ($1, 'booking_cancelled', '{"actor_id":"00000000-0000-4000-8000-000000000001","trip_id":"00000000-0000-4000-8000-000000000002","booking_id":"00000000-0000-4000-8000-000000000003"}'::jsonb, now() - interval '31 days', now() - interval '31 days')`, passenger.userID)
	e.exec(`INSERT INTO notifications (user_id, type, payload, read_at, created_at)
		VALUES ($1, 'booking_cancelled', '{"trip_id":"00000000-0000-4000-8000-000000000004"}'::jsonb, now() - interval '1 day', now() - interval '1 day')`, passenger.userID)
	e.exec(`INSERT INTO notifications (user_id, type, payload, created_at)
		VALUES ($1, 'trip_cancelled', '{"trip_id":"00000000-0000-4000-8000-000000000005"}'::jsonb, now() - interval '40 days')`, passenger.userID)

	stats, err := e.jobs.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.CancelledPending)
	require.Equal(t, int64(1), stats.DeletedNotifications)
	require.Equal(t, "cancelled_by_driver", e.queryString(`SELECT status::text FROM bookings WHERE id = $1`, pastBooked.str("id")))
	require.Equal(t, "pending", e.queryString(`SELECT status::text FROM bookings WHERE id = $1`, futureBooked.str("id")))
	require.Equal(t, "expired", e.queryString(`SELECT status::text FROM trips WHERE id = $1`, past))
	payload := e.queryString(`SELECT payload::text FROM notifications WHERE user_id = $1 AND type = 'booking_cancelled' AND payload ? 'booking_id' ORDER BY created_at DESC LIMIT 1`, passenger.userID)
	require.NotContains(t, payload, "+993")
	require.NotContains(t, payload, "phone")
	require.Contains(t, payload, driver.userID)
	require.Equal(t, 0, e.queryInt(`SELECT count(*) FROM notifications WHERE user_id = $1 AND read_at < now() - interval '30 days'`, passenger.userID))
	require.Equal(t, 1, e.queryInt(`SELECT count(*) FROM notifications WHERE user_id = $1 AND read_at > now() - interval '2 days'`, passenger.userID))
	require.Equal(t, 1, e.queryInt(`SELECT count(*) FROM notifications WHERE user_id = $1 AND type = 'trip_cancelled' AND read_at IS NULL`, passenger.userID))
}

type failNotifier struct{}

func (failNotifier) Notify(context.Context, ...notify.Event) error {
	return errors.New("inbox down")
}

func (e *env) completedPair(driver, passenger session) string {
	e.t.Helper()
	tripID := e.createTrip(driver.access, locAshgabat, locMaryCity, 2, 24*time.Hour)
	booked := e.call(http.MethodPost, "/v1/trips/"+tripID+"/bookings", map[string]any{"seats": 1}, authEN(passenger.access))
	require.Equal(e.t, http.StatusCreated, booked.Status, string(booked.Body))
	require.Equal(e.t, http.StatusOK, e.call(http.MethodPost, "/v1/bookings/"+booked.str("id")+"/approve", nil, authEN(driver.access)).Status)
	e.completeTrip(driver.access, tripID)
	return tripID
}

func (e *env) completeTrip(token, tripID string) {
	e.t.Helper()
	e.exec(`UPDATE trips SET departs_at = now() - interval '1 hour' WHERE id = $1`, tripID)
	done := e.call(http.MethodPost, "/v1/trips/"+tripID+"/complete", nil, authEN(token))
	require.Equal(e.t, http.StatusOK, done.Status, string(done.Body))
	require.Equal(e.t, "completed", done.str("status"))
}
