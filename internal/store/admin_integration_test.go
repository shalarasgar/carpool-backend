package store

import (
	"context"
	"errors"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// letterTag returns a random run of letters. Names built from it are unique to the test
// and, having no digits, never match a phone search by accident.
func letterTag(prefix string) string {
	b := []byte(prefix)
	for range 10 {
		b = append(b, byte('a'+rand.IntN(26)))
	}
	return string(b) + "x"
}

func adminSearch(t *testing.T, db *AppDB, text string, state ride.UserState) map[string]ride.AdminUser {
	t.Helper()
	q := ride.UserQuery{Text: text, State: state}
	if err := q.Normalize(); err != nil {
		t.Fatal(err)
	}
	found := map[string]ride.AdminUser{}
	for page := 1; ; page++ {
		list, more, err := db.AdminUsers(context.Background(), q, page, 50)
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range list {
			found[u.ID] = u
		}
		if !more {
			return found
		}
	}
}

func TestAdminCardCountsActivityAndReports(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	now := time.Now()
	tag := letterTag("Adm")
	driver, driverPhone := mkUser(t, db, tag+"Driver")
	p1, _ := mkUser(t, db, tag+"Pa")
	p2, _ := mkUser(t, db, tag+"Pb")
	trip := mkTrip(t, db, driver, tripOpts{})

	// p1 withdraws a request; the driver cancels p2 after approving.
	b1 := mustBook(t, db, trip, p1, 0, 2, 1)
	if err := db.CancelBooking(ctx, p1, b1, now); err != nil {
		t.Fatal(err)
	}
	b2 := mustBook(t, db, trip, p2, 0, 2, 1)
	if err := db.DecideBooking(ctx, driver, b2, true, now); err != nil {
		t.Fatal(err)
	}
	if err := db.CancelBooking(ctx, driver, b2, now); err != nil {
		t.Fatal(err)
	}
	b3 := mustBook(t, db, trip, p2, 0, 1, 1)

	// Three reports about the driver from two people, one report about p2.
	for _, in := range []ride.ReportInput{
		{ReporterID: p1, ReportedUserID: driver, TripID: trip, Reason: ride.ReasonUnsafe},
		{ReporterID: p1, ReportedUserID: driver, Reason: ride.ReasonAbuse},
		{ReporterID: p2, ReportedUserID: driver, Reason: ride.ReasonUnsafe},
		{ReporterID: driver, ReportedUserID: p2, Reason: ride.ReasonNoShow},
	} {
		if _, err := db.CreateReport(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.BlockUser(ctx, p1, driver); err != nil {
		t.Fatal(err)
	}

	d, err := db.AdminUser(ctx, driver)
	if err != nil {
		t.Fatal(err)
	}
	want := ride.UserActivity{TripsUpcoming: 1, DriverCancellations: 1, ReportsAgainst: 3, ReportsAgainstOpen: 3,
		Reporters: 2, ReportsFiled: 1, BlockedBy: 1}
	if d.Activity != want {
		t.Fatalf("driver activity = %+v, want %+v", d.Activity, want)
	}
	if d.Phone != driverPhone || d.FirstName != tag+"Driver" || d.OpenReports != 3 || d.Blocked || d.Deleted ||
		d.Locale == "" || len(d.Vehicles) != 1 || d.Vehicles[0].Plate == "" {
		t.Fatalf("driver card = %+v", d)
	}

	pa, err := db.AdminUser(ctx, p1)
	if err != nil {
		t.Fatal(err)
	}
	if pa.Activity != (ride.UserActivity{PassengerCancellations: 1, ReportsFiled: 2}) {
		t.Fatalf("p1 activity = %+v", pa.Activity)
	}
	pb, err := db.AdminUser(ctx, p2)
	if err != nil {
		t.Fatal(err)
	}
	if pb.Activity != (ride.UserActivity{BookingsActive: 1, ReportsAgainst: 1, ReportsAgainstOpen: 1, Reporters: 1, ReportsFiled: 1}) {
		t.Fatalf("p2 activity = %+v (booking %s)", pb.Activity, bookingStatus(t, db, b3))
	}

	if _, err := db.AdminUser(ctx, "00000000-0000-4000-8000-000000000000"); !errors.Is(err, ride.ErrNotFound) {
		t.Fatalf("unknown user err = %v", err)
	}
	if _, err := db.AdminUser(ctx, "nope"); !errors.Is(err, ride.ErrNotFound) {
		t.Fatalf("malformed id err = %v", err)
	}

	// Report filters and the reported user's history on every row.
	list, _, err := db.Reports(ctx, ride.ReportFilter{ReportedUserID: driver}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("reports about the driver = %d", len(list))
	}
	for _, r := range list {
		if r.ReportedReports != 3 || r.ReportedOpenReports != 3 || r.ReportedReporters != 2 {
			t.Fatalf("history = %+v", r)
		}
	}
	list, _, err = db.Reports(ctx, ride.ReportFilter{ReportedUserID: driver, ReporterID: p1, Reason: ride.ReasonUnsafe}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].TripID != trip {
		t.Fatalf("filtered reports = %+v", list)
	}
	if err := db.ResolveReport(ctx, list[0].ID, now); err != nil {
		t.Fatal(err)
	}
	list, _, err = db.Reports(ctx, ride.ReportFilter{Status: ride.ReportOpen, ReportedUserID: driver}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ReportedReports != 3 || list[0].ReportedOpenReports != 2 {
		t.Fatalf("open reports after resolving one = %+v", list)
	}
}

func TestAdminSearchFindsByNameAndPhone(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	tag := letterTag("Srch")
	a, aPhone := mkUser(t, db, tag+"Ann")
	b, _ := mkUser(t, db, tag+"Bob")
	c, _ := mkUser(t, db, tag+"Cem")

	if got := adminSearch(t, db, tag, ""); len(got) != 3 {
		t.Fatalf("name search = %d users", len(got))
	}
	if got := adminSearch(t, db, tag+"ann test", ""); len(got) != 1 || got[a].Phone != aPhone {
		t.Fatalf("full name search, any case = %+v", got)
	}
	// The local part of the number, typed with spaces, finds the account.
	local := aPhone[4:]
	if got := adminSearch(t, db, local[:2]+" "+local[2:], ""); got[a].ID != a {
		t.Fatalf("phone search missed %s", aPhone)
	}
	if got := adminSearch(t, db, "%_"+tag, ""); len(got) != 0 {
		t.Fatalf("wildcards were not escaped: %+v", got)
	}

	if err := db.SetUserBlocked(ctx, b, true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DeleteAccount(ctx, c, time.Now()); err != nil {
		t.Fatal(err)
	}
	live := adminSearch(t, db, tag, "")
	if len(live) != 2 || !live[b].Blocked {
		t.Fatalf("live accounts = %+v", live)
	}
	if got := adminSearch(t, db, tag, ride.UserStateActive); len(got) != 1 || got[a].ID != a {
		t.Fatalf("active accounts = %+v", got)
	}
	if got := adminSearch(t, db, tag, ride.UserStateBlocked); len(got) != 1 || got[b].ID != b {
		t.Fatalf("blocked accounts = %+v", got)
	}
	if got := adminSearch(t, db, "", ride.UserStateDeleted); !got[c].Deleted {
		t.Fatal("the deleted account is missing from the deleted list")
	}
	d, err := db.AdminUser(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Deleted || d.DeletedAt == nil || d.Phone != "" {
		t.Fatalf("deleted card = %+v", d)
	}
}

func TestAdminOverviewCountsToday(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	since := time.Now().Add(-time.Minute)
	driver, _ := mkUser(t, db, "OverviewDriver")
	p, _ := mkUser(t, db, "OverviewPassenger")
	trip := mkTrip(t, db, driver, tripOpts{})
	mustBook(t, db, trip, p, 0, 1, 1)
	if _, err := db.CreateReport(ctx, ride.ReportInput{ReporterID: p, ReportedUserID: driver, Reason: ride.ReasonSpam}); err != nil {
		t.Fatal(err)
	}

	o, err := db.Overview(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if !o.Since.Equal(since) || o.UsersNew < 2 || o.UsersTotal < 2 || o.TripsNew < 1 ||
		o.TripsUpcoming < 1 || o.BookingsNew < 1 || o.BookingsPending < 1 || o.ReportsOpen < 1 {
		t.Fatalf("overview = %+v", o)
	}
	future, err := db.Overview(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if future.UsersNew != 0 || future.TripsNew != 0 || future.BookingsNew != 0 || future.SMSFailed != 0 {
		t.Fatalf("counts from a future start = %+v", future)
	}
}
