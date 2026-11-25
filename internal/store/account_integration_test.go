package store

// Integration tests of account deletion, user blocks, reports, and the review login.
// They need CARPOOL_TEST_DATABASE_URL, like the rest of the store integration tests.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/auth"
	"github.com/shalarasgar/carpool-backend/internal/ride"
)

type userRow struct {
	phone     *string
	firstName string
	lastName  string
	photo     *string
	ratingN   int
	deleted   bool
	blocked   bool
}

func loadUserRow(t *testing.T, db *AppDB, id string) userRow {
	t.Helper()
	var u userRow
	if err := db.pool.QueryRow(context.Background(), `
		SELECT phone, first_name, last_name, photo_url, rating_count, deleted_at IS NOT NULL, is_blocked
		FROM users WHERE id = $1::uuid
	`, id).Scan(&u.phone, &u.firstName, &u.lastName, &u.photo, &u.ratingN, &u.deleted, &u.blocked); err != nil {
		t.Fatal(err)
	}
	return u
}

func countRows(t *testing.T, db *AppDB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDeleteAccountErasesADriver(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	now := time.Now()
	driver, phone := mkUser(t, db, "Driver")
	approved, _ := mkUser(t, db, "Approved")
	pending, _ := mkUser(t, db, "Pending")
	trip := mkTrip(t, db, driver, tripOpts{})
	ab := mustBook(t, db, trip, approved, 0, 2, 1)
	pb := mustBook(t, db, trip, pending, 0, 1, 1)
	if err := db.DecideBooking(ctx, driver, ab, true, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetUserPhoto(ctx, driver, "/media/user_abc.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := db.SavePushToken(ctx, driver, "token-"+driver, "android", now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE vehicles SET photo_url = '/media/vehicle_abc.jpg' WHERE user_id = $1::uuid`, driver); err != nil {
		t.Fatal(err)
	}

	files, err := db.DeleteAccount(ctx, driver, now)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"/media/user_abc.jpg": false, "/media/vehicle_abc.jpg": false}
	for _, f := range files {
		if _, ok := want[f]; ok {
			want[f] = true
		}
	}
	for f, seen := range want {
		if !seen {
			t.Fatalf("media file %s was not returned for removal: %v", f, files)
		}
	}

	u := loadUserRow(t, db, driver)
	if !u.deleted || u.phone != nil || u.firstName != "" || u.lastName != "" || u.photo != nil {
		t.Fatalf("user row after deletion = %+v", u)
	}
	if s := tripStatus(t, db, trip); s != "cancelled" {
		t.Fatalf("trip status = %s", s)
	}
	for _, b := range []string{ab, pb} {
		if s := bookingStatus(t, db, b); s != "cancelled_by_driver" {
			t.Fatalf("booking status = %s", s)
		}
	}
	for _, p := range []string{approved, pending} {
		if n := countNotes(t, db, p, "trip_cancelled"); n != 1 {
			t.Fatalf("trip_cancelled notifications = %d", n)
		}
	}
	for name, n := range map[string]int{
		"vehicles":    countRows(t, db, `SELECT COUNT(*) FROM vehicles WHERE user_id = $1::uuid`, driver),
		"push tokens": countRows(t, db, `SELECT COUNT(*) FROM push_tokens WHERE user_id = $1::uuid`, driver),
		"inbox":       countRows(t, db, `SELECT COUNT(*) FROM notifications WHERE user_id = $1::uuid`, driver),
		"sessions":    countRows(t, db, `SELECT COUNT(*) FROM refresh_tokens WHERE user_id = $1::uuid`, driver),
	} {
		if n != 0 {
			t.Fatalf("%s left after deletion: %d", name, n)
		}
	}

	if err := db.ActiveUser(ctx, driver); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("active user err = %v", err)
	}
	if _, err := db.PublicProfile(ctx, driver); !errors.Is(err, ride.ErrNotFound) {
		t.Fatalf("public profile err = %v", err)
	}
	if _, err := db.DeleteAccount(ctx, driver, now); !errors.Is(err, ride.ErrNotFound) {
		t.Fatalf("second deletion err = %v", err)
	}
	// The passenger still sees the cancelled booking, with the driver shown as deleted.
	b, err := db.Booking(ctx, approved, ab)
	if err != nil {
		t.Fatal(err)
	}
	if b.Trip == nil || !b.Trip.Driver.Deleted || b.Trip.Driver.FirstName != "" || b.ContactPhone != "" {
		t.Fatalf("booking after driver deletion = %+v", b)
	}
	// The number is free again.
	if _, err := db.pool.Exec(ctx, `INSERT INTO users (phone) VALUES ($1)`, phone); err != nil {
		t.Fatalf("register the released number: %v", err)
	}
}

func TestDeleteAccountCancelsAPassengersBookings(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	now := time.Now()
	driver, _ := mkUser(t, db, "Driver")
	passenger, _ := mkUser(t, db, "Passenger")
	full := mkTrip(t, db, driver, tripOpts{seats: 2})
	fb := mustBook(t, db, full, passenger, 0, 2, 2)
	if err := db.DecideBooking(ctx, driver, fb, true, now); err != nil {
		t.Fatal(err)
	}
	if s := tripStatus(t, db, full); s != "full" {
		t.Fatalf("trip status before deletion = %s", s)
	}
	other, _ := mkUser(t, db, "OtherDriver")
	open := mkTrip(t, db, other, tripOpts{})
	ob := mustBook(t, db, open, passenger, 0, 1, 1)

	if _, err := db.DeleteAccount(ctx, passenger, now); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{fb, ob} {
		if s := bookingStatus(t, db, b); s != "cancelled_by_passenger" {
			t.Fatalf("booking status = %s", s)
		}
	}
	if s := tripStatus(t, db, full); s != "active" {
		t.Fatalf("seats were not freed: trip status = %s", s)
	}
	if load := segmentLoad(t, db, full); load[0] != 0 || load[1] != 0 {
		t.Fatalf("segment load = %v", load)
	}
	for _, d := range []string{driver, other} {
		if n := countNotes(t, db, d, "booking_cancelled_by_passenger"); n != 1 {
			t.Fatalf("driver notifications = %d", n)
		}
	}
	// The drivers' accounts and trips are untouched.
	if s := tripStatus(t, db, open); s != "active" {
		t.Fatalf("other trip status = %s", s)
	}
}

func TestDeleteAccountWaitsForATripInProgress(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	driver, phone := mkUser(t, db, "Driver")
	passenger, _ := mkUser(t, db, "Passenger")
	trip := mkTrip(t, db, driver, tripOpts{})
	b := mustBook(t, db, trip, passenger, 0, 2, 1)
	if err := db.DecideBooking(ctx, driver, b, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	later := mkTrip(t, db, driver, tripOpts{departs: time.Now().Add(96 * time.Hour).Truncate(time.Minute)})
	departTripInPast(t, db, trip)

	if _, err := db.DeleteAccount(ctx, driver, time.Now()); !errors.Is(err, ride.ErrTripInProgress) {
		t.Fatalf("deletion during a trip err = %v", err)
	}
	// Nothing was changed by the refused attempt, including the later trip.
	if u := loadUserRow(t, db, driver); u.deleted || u.phone == nil || *u.phone != phone {
		t.Fatalf("user row after refused deletion = %+v", u)
	}
	if s := tripStatus(t, db, later); s != "active" {
		t.Fatalf("later trip status = %s", s)
	}

	// The passenger of a departed trip can still delete; the booking completes with the trip.
	if _, err := db.DeleteAccount(ctx, passenger, time.Now()); err != nil {
		t.Fatalf("passenger deletion: %v", err)
	}
	if s := bookingStatus(t, db, b); s != "approved" {
		t.Fatalf("booking on a departed trip = %s", s)
	}
	if err := db.CompleteTrip(ctx, driver, trip, time.Now()); err != nil {
		t.Fatal(err)
	}
	if s := bookingStatus(t, db, b); s != "completed" {
		t.Fatalf("booking after completion = %s", s)
	}
	// A deleted account cannot be reviewed.
	if _, err := db.CreateReview(ctx, ride.ReviewInput{BookingID: b, AuthorID: driver, Rating: 5}); !errors.Is(err, ride.ErrReviewNotAllowed) {
		t.Fatalf("review of a deleted account err = %v", err)
	}
	if _, err := db.DeleteAccount(ctx, driver, time.Now()); err != nil {
		t.Fatalf("deletion after completion: %v", err)
	}
	if s := tripStatus(t, db, later); s != "cancelled" {
		t.Fatalf("later trip status after deletion = %s", s)
	}
}

func TestDeleteAccountHandlesReviews(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	driver, _ := mkUser(t, db, "Driver")
	passenger, _ := mkUser(t, db, "Passenger")
	trip := mkTrip(t, db, driver, tripOpts{})
	b := mustBook(t, db, trip, passenger, 0, 2, 1)
	if err := db.DecideBooking(ctx, driver, b, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	departTripInPast(t, db, trip)
	if err := db.CompleteTrip(ctx, driver, trip, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, author := range []string{driver, passenger} {
		if _, err := db.CreateReview(ctx, ride.ReviewInput{BookingID: b, AuthorID: author, Rating: 4, Comment: "note"}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := db.DeleteAccount(ctx, passenger, time.Now()); err != nil {
		t.Fatal(err)
	}
	// The review about the passenger is gone with the account.
	if n := countRows(t, db, `SELECT COUNT(*) FROM reviews WHERE to_user_id = $1::uuid`, passenger); n != 0 {
		t.Fatalf("reviews about the deleted user = %d", n)
	}
	// The rating the passenger gave the driver stays, without the comment.
	reviews, _, err := db.UserReviews(ctx, driver, 1, 10)
	if err != nil || len(reviews) != 1 {
		t.Fatalf("driver reviews = %+v err=%v", reviews, err)
	}
	if r := reviews[0]; r.Rating != 4 || r.Comment != "" || !r.Author.Deleted {
		t.Fatalf("kept review = %+v", r)
	}
	if p, err := db.PublicProfile(ctx, driver); err != nil || p.RatingCount != 1 {
		t.Fatalf("driver rating count = %d err=%v", p.RatingCount, err)
	}
}

func TestBannedAccountKeepsItsNumberWhenDeleted(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	user, phone := mkUser(t, db, "Banned")
	reporter, _ := mkUser(t, db, "Reporter")
	if _, err := db.CreateReport(ctx, ride.ReportInput{ReporterID: reporter, ReportedUserID: user, Reason: ride.ReasonFraud}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetUserBlocked(ctx, user, true); err != nil {
		t.Fatal(err)
	}
	if err := db.ActiveUser(ctx, user); !errors.Is(err, auth.ErrBlocked) {
		t.Fatalf("banned user err = %v", err)
	}
	if _, err := db.DeleteAccount(ctx, user, time.Now()); err != nil {
		t.Fatal(err)
	}
	u := loadUserRow(t, db, user)
	if !u.deleted || !u.blocked || u.phone == nil || *u.phone != phone || u.firstName != "" {
		t.Fatalf("banned and deleted row = %+v", u)
	}
	// The report that justified the ban is kept while the ban stands.
	if n := countRows(t, db, `SELECT COUNT(*) FROM reports WHERE reported_user_id = $1::uuid`, user); n != 1 {
		t.Fatalf("reports about the banned user = %d", n)
	}
	// Lifting the ban releases the number.
	if err := db.SetUserBlocked(ctx, user, false); err != nil {
		t.Fatal(err)
	}
	if u := loadUserRow(t, db, user); u.phone != nil || u.blocked {
		t.Fatalf("row after the ban was lifted = %+v", u)
	}
	if err := db.SetUserBlocked(ctx, "00000000-0000-4000-8000-000000000000", true); !errors.Is(err, ride.ErrNotFound) {
		t.Fatalf("ban of an unknown user err = %v", err)
	}
}

func TestUserBlocksHideTripsAndStopBookings(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	driver, _ := mkUser(t, db, "Driver")
	passenger, _ := mkUser(t, db, "Passenger")
	trip := mkTrip(t, db, driver, tripOpts{})

	if err := db.BlockUser(ctx, passenger, "00000000-0000-4000-8000-000000000000"); !errors.Is(err, ride.ErrNotFound) {
		t.Fatalf("block of an unknown user err = %v", err)
	}
	for range 2 { // blocking twice is a no-op
		if err := db.BlockUser(ctx, passenger, driver); err != nil {
			t.Fatal(err)
		}
	}
	list, err := db.BlockedUsers(ctx, passenger)
	if err != nil || len(list) != 1 || list[0].ID != driver || list[0].FirstName != "Driver" {
		t.Fatalf("blocked list = %+v err=%v", list, err)
	}
	if _, err := book(t, db, trip, passenger, 0, 2, 1); !errors.Is(err, ride.ErrUserBlocked) {
		t.Fatalf("booking across a block err = %v", err)
	}
	found, _, err := db.SearchTrips(ctx, ride.SearchQuery{ViewerID: passenger, FromID: ashgabat, ToID: mary, Seats: 1, Page: 1, Limit: 50}, time.Now(), "Asia/Ashgabat")
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range found {
		if tr.ID == trip {
			t.Fatal("a blocked driver's trip is still in search results")
		}
	}

	for range 2 { // unblocking twice is a no-op
		if err := db.UnblockUser(ctx, passenger, driver); err != nil {
			t.Fatal(err)
		}
	}
	if list, err := db.BlockedUsers(ctx, passenger); err != nil || len(list) != 0 {
		t.Fatalf("blocked list after unblock = %+v err=%v", list, err)
	}
	if _, err := book(t, db, trip, passenger, 0, 2, 1); err != nil {
		t.Fatalf("booking after unblock: %v", err)
	}
}

func TestReportsAreQueuedAndResolved(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	reporter, _ := mkUser(t, db, "Reporter")
	reported, _ := mkUser(t, db, "Reported")
	trip := mkTrip(t, db, reported, tripOpts{})

	if _, err := db.CreateReport(ctx, ride.ReportInput{ReporterID: reporter, ReportedUserID: "00000000-0000-4000-8000-000000000000", Reason: ride.ReasonSpam}); !errors.Is(err, ride.ErrNotFound) {
		t.Fatalf("report of an unknown user err = %v", err)
	}
	var field *ride.FieldError
	if _, err := db.CreateReport(ctx, ride.ReportInput{ReporterID: reporter, ReportedUserID: reported, TripID: "00000000-0000-4000-8000-000000000000", Reason: ride.ReasonSpam}); !errors.As(err, &field) || field.Field != "trip_id" {
		t.Fatalf("report with an unknown trip err = %v", err)
	}
	id, err := db.CreateReport(ctx, ride.ReportInput{ReporterID: reporter, ReportedUserID: reported, TripID: trip, Reason: ride.ReasonUnsafe, Details: "drove too fast"})
	if err != nil {
		t.Fatal(err)
	}

	find := func(status ride.ReportStatus) *ride.Report {
		t.Helper()
		for page := 1; ; page++ {
			list, more, err := db.Reports(ctx, ride.ReportFilter{Status: status}, page, 50)
			if err != nil {
				t.Fatal(err)
			}
			for i := range list {
				if list[i].ID == id {
					return &list[i]
				}
			}
			if !more {
				return nil
			}
		}
	}
	r := find(ride.ReportOpen)
	if r == nil {
		t.Fatal("new report is not in the open queue")
	}
	if r.ReporterName != "Reporter Test" || r.ReportedName != "Reported Test" || r.TripID != trip ||
		r.Reason != ride.ReasonUnsafe || r.Details != "drove too fast" || r.Status != ride.ReportOpen || r.ResolvedAt != nil {
		t.Fatalf("report = %+v", r)
	}

	first := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := db.ResolveReport(ctx, id, first); err != nil {
		t.Fatal(err)
	}
	if err := db.ResolveReport(ctx, id, time.Now()); err != nil {
		t.Fatalf("resolving twice: %v", err)
	}
	if find(ride.ReportOpen) != nil {
		t.Fatal("resolved report is still in the open queue")
	}
	r = find(ride.ReportResolved)
	if r == nil || r.ResolvedAt == nil || !r.ResolvedAt.Equal(first) {
		t.Fatalf("resolved report = %+v", r)
	}
	if find("") == nil {
		t.Fatal("report is missing from the unfiltered list")
	}
	if err := db.ResolveReport(ctx, "00000000-0000-4000-8000-000000000000", time.Now()); !errors.Is(err, ride.ErrNotFound) {
		t.Fatalf("resolve of an unknown report err = %v", err)
	}

	// A report the user filed outlives their account; a report about them does not.
	if _, err := db.DeleteAccount(ctx, reporter, time.Now()); err != nil {
		t.Fatal(err)
	}
	if find("") == nil {
		t.Fatal("report disappeared with the reporter's account")
	}
	if _, err := db.DeleteAccount(ctx, reported, time.Now()); err != nil {
		t.Fatal(err)
	}
	if find("") != nil {
		t.Fatal("report about a deleted account was kept")
	}
}

func TestReviewLoginStoresACodeWithoutAnSMS(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	phone := randomPhone()
	const secret = "integration-test-otp-secret"
	queued := func() int {
		return countRows(t, db, `SELECT COUNT(*) FROM sms_queue WHERE phone = $1`, phone)
	}
	challenge := auth.Challenge{
		Phone: phone, CodeHash: auth.HashOTP(secret, "246810"), Message: "unused",
		ExpiresAt: time.Now().Add(5 * time.Minute), Now: time.Now(),
		PhoneInterval: time.Minute, PhoneDailyMax: 1, IPHourlyMax: 1000, DeviceHourlyMax: 1000,
		SkipPhoneLimits: true, NoSMS: true,
	}
	// Twice in a row: the per-phone interval and daily quota do not apply.
	for range 2 {
		res, err := db.CreateChallenge(ctx, challenge)
		if err != nil {
			t.Fatal(err)
		}
		if res.NoHealthyGateway {
			t.Fatal("a challenge without an SMS reported a missing gateway")
		}
	}
	if n := queued(); n != 0 {
		t.Fatalf("sms rows for the review number = %d", n)
	}
	user, err := db.VerifyOTP(ctx, auth.VerifySpec{
		Phone: phone, DeviceID: "review-device", Locale: "en", Now: time.Now(), MaxAttempts: 5,
		RefreshHash: auth.HashSecret("review-refresh-" + phone), RefreshExpiry: time.Now().Add(time.Hour),
		Match: func(s string) bool { return auth.OTPEqual(secret, "246810", s) },
	})
	if err != nil || user.Phone != phone {
		t.Fatalf("verify = %+v err=%v", user, err)
	}

	// The quotas shared with other numbers still apply.
	limited := challenge
	limited.IP, limited.IPHourlyMax = "203.0.113.200", 1
	if _, err := db.pool.Exec(ctx, `DELETE FROM otp_codes WHERE ip = $1`, limited.IP); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateChallenge(ctx, limited); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateChallenge(ctx, limited); err == nil {
		t.Fatal("the per-IP quota was skipped for the review number")
	}
}
