package store

// Integration tests against a real PostgreSQL. They run when CARPOOL_TEST_DATABASE_URL is
// set, for example:
//
//	CARPOOL_TEST_DATABASE_URL=postgres://carpool:carpool@localhost:5432/carpool_test?sslmode=disable go test ./internal/store/
//
// Every test creates its own users, so the database can be reused between runs.

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shalarasgar/carpool-backend/internal/auth"
	appmigrate "github.com/shalarasgar/carpool-backend/internal/migrate"
	"github.com/shalarasgar/carpool-backend/internal/ride"
)

const (
	ashgabat = "c2000000-0000-4000-8000-000000000001"
	tejen    = "c2000000-0000-4000-8000-000000000003"
	mary     = "c2000000-0000-4000-8000-000000000008"
	ahal     = "a1000000-0000-4000-8000-000000000001"
)

var (
	setupOnce sync.Once
	testPool  *pgxpool.Pool
	setupErr  error
)

func integrationDB(t *testing.T) *AppDB {
	t.Helper()
	url := os.Getenv("CARPOOL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("CARPOOL_TEST_DATABASE_URL is not set")
	}
	setupOnce.Do(func() {
		if setupErr = appmigrate.Up(url); setupErr != nil {
			return
		}
		cfg, err := pgxpool.ParseConfig(url)
		if err != nil {
			setupErr = err
			return
		}
		cfg.MaxConns = 30
		testPool, setupErr = pgxpool.NewWithConfig(context.Background(), cfg)
	})
	if setupErr != nil {
		t.Fatalf("setup: %v", setupErr)
	}
	return NewAppDB(testPool)
}

func randomPhone() string {
	return fmt.Sprintf("+9936%07d", rand.IntN(10000000))
}

func mkUser(t *testing.T, db *AppDB, first string) (string, string) {
	t.Helper()
	for range 5 {
		phone := randomPhone()
		var id string
		err := db.pool.QueryRow(context.Background(), `
			INSERT INTO users (phone, first_name, last_name, name) VALUES ($1, $2, 'Test', $2 || ' Test')
			RETURNING id::text
		`, phone, first).Scan(&id)
		if isUnique(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		return id, phone
	}
	t.Fatal("could not create a user with a unique phone")
	return "", ""
}

func mkVehicle(t *testing.T, db *AppDB, userID string, seats int) string {
	t.Helper()
	v, err := db.CreateVehicle(context.Background(), ride.Vehicle{
		UserID: userID, Brand: "Toyota", Model: "Camry", Color: "White", Plate: "AG 1234", PassengerSeats: &seats,
	})
	if err != nil {
		t.Fatal(err)
	}
	return v.ID
}

type tripOpts struct {
	stops   []string
	seats   int
	departs time.Time
	hours   int
}

func mkTrip(t *testing.T, db *AppDB, driverID string, o tripOpts) string {
	t.Helper()
	if o.seats == 0 {
		o.seats = 4
	}
	if len(o.stops) == 0 {
		o.stops = []string{ashgabat, tejen, mary}
	}
	if o.departs.IsZero() {
		o.departs = time.Now().Add(48 * time.Hour).Truncate(time.Minute)
	}
	if o.hours == 0 {
		o.hours = 6
	}
	in := ride.TripInput{
		DriverID: driverID, VehicleID: mkVehicle(t, db, driverID, 8),
		DepartsAt: o.departs, ArrivesAt: o.departs.Add(time.Duration(o.hours) * time.Hour), SeatsTotal: o.seats,
	}
	for _, s := range o.stops {
		in.Stops = append(in.Stops, ride.Stop{LocationID: s})
	}
	if err := in.Normalize(time.Now()); err != nil {
		t.Fatal(err)
	}
	id, err := db.CreateTrip(context.Background(), in, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func book(t *testing.T, db *AppDB, tripID, passengerID string, from, to, seats int) (string, error) {
	t.Helper()
	in := ride.BookingInput{PassengerID: passengerID, TripID: tripID, PickupOrder: from, DropoffOrder: to, Seats: seats}
	if err := in.Normalize(); err != nil {
		t.Fatal(err)
	}
	return db.CreateBooking(context.Background(), in, time.Now())
}

func mustBook(t *testing.T, db *AppDB, tripID, passengerID string, from, to, seats int) string {
	t.Helper()
	id, err := book(t, db, tripID, passengerID, from, to, seats)
	if err != nil {
		t.Fatalf("book %d->%d x%d: %v", from, to, seats, err)
	}
	return id
}

// segmentLoad reads the approved load per segment straight from the database.
func segmentLoad(t *testing.T, db *AppDB, tripID string) []int {
	t.Helper()
	rows, err := db.pool.Query(context.Background(), `
		SELECT g.s, COALESCE(SUM(b.seats), 0)::int
		FROM generate_series(0, (SELECT MAX(stop_order) - 1 FROM trip_stops WHERE trip_id = $1::uuid)) g(s)
		LEFT JOIN bookings b ON b.trip_id = $1::uuid AND b.status = 'approved'
		     AND b.pickup_order <= g.s AND b.dropoff_order > g.s
		GROUP BY g.s ORDER BY g.s
	`, tripID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var s, l int
		if err := rows.Scan(&s, &l); err != nil {
			t.Fatal(err)
		}
		out = append(out, l)
	}
	return out
}

func tripStatus(t *testing.T, db *AppDB, id string) string {
	t.Helper()
	var s string
	if err := db.pool.QueryRow(context.Background(), `SELECT status::text FROM trips WHERE id = $1::uuid`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func bookingStatus(t *testing.T, db *AppDB, id string) string {
	t.Helper()
	var s string
	if err := db.pool.QueryRow(context.Background(), `SELECT status::text FROM bookings WHERE id = $1::uuid`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func countNotes(t *testing.T, db *AppDB, userID, typ string) int {
	t.Helper()
	var n int
	if err := db.pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM notifications WHERE user_id = $1::uuid AND type = $2
	`, userID, typ).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The scenario from the specification: four seats, A asks for 2, B for 3, the driver
// approves both at the same moment. Exactly one approval may succeed.
func TestConcurrentApprovalsNeverOverbook(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	for round := range 10 {
		driver, _ := mkUser(t, db, "Driver")
		a, _ := mkUser(t, db, "Aman")
		b, _ := mkUser(t, db, "Bahar")
		trip := mkTrip(t, db, driver, tripOpts{stops: []string{ashgabat, mary}})
		ba := mustBook(t, db, trip, a, 0, 1, 2)
		bb := mustBook(t, db, trip, b, 0, 1, 3)
		if s := bookingStatus(t, db, ba); s != "pending" {
			t.Fatalf("pending requests must not hold seats, status = %s", s)
		}

		var wg sync.WaitGroup
		errs := make([]error, 2)
		start := make(chan struct{})
		for i, id := range []string{ba, bb} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs[i] = db.DecideBooking(ctx, driver, id, true, time.Now())
			}()
		}
		close(start)
		wg.Wait()

		ok := 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ride.ErrInsufficientSeats):
			default:
				t.Fatalf("round %d: unexpected error %v", round, err)
			}
		}
		if ok != 1 {
			t.Fatalf("round %d: %d approvals succeeded, want 1 (errs %v)", round, ok, errs)
		}
		if load := segmentLoad(t, db, trip); load[0] > 4 {
			t.Fatalf("round %d: overbooked, load = %v", round, load)
		}
	}
}

func TestManyParallelApprovalsFillExactlyTheCapacity(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	driver, _ := mkUser(t, db, "Driver")
	trip := mkTrip(t, db, driver, tripOpts{stops: []string{ashgabat, mary}, seats: 4})
	var ids []string
	for i := range 12 {
		p, _ := mkUser(t, db, fmt.Sprintf("P%d", i))
		ids = append(ids, mustBook(t, db, trip, p, 0, 1, 1))
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	approved := 0
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := db.DecideBooking(ctx, driver, id, true, time.Now())
			if err == nil {
				mu.Lock()
				approved++
				mu.Unlock()
			} else if !errors.Is(err, ride.ErrInsufficientSeats) {
				t.Errorf("approve: %v", err)
			}
		}()
	}
	wg.Wait()
	if approved != 4 {
		t.Fatalf("approved = %d, want 4", approved)
	}
	if load := segmentLoad(t, db, trip); load[0] != 4 {
		t.Fatalf("load = %v", load)
	}
	if s := tripStatus(t, db, trip); s != "full" {
		t.Fatalf("trip status = %s, want full", s)
	}
}

func TestSegmentCapacityAcrossIntermediateStops(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	driver, _ := mkUser(t, db, "Driver")
	trip := mkTrip(t, db, driver, tripOpts{}) // Ashgabat(0) -> Tejen(1) -> Mary(2), 4 seats
	pa, _ := mkUser(t, db, "A")
	pb, _ := mkUser(t, db, "B")
	pc, _ := mkUser(t, db, "C")
	pd, _ := mkUser(t, db, "D")

	ba := mustBook(t, db, trip, pa, 0, 1, 2) // Ashgabat -> Tejen
	bb := mustBook(t, db, trip, pb, 1, 2, 3) // Tejen -> Mary
	bc := mustBook(t, db, trip, pc, 0, 2, 3) // Ashgabat -> Mary, overlaps both
	for _, id := range []string{ba, bb} {
		if err := db.DecideBooking(ctx, driver, id, true, time.Now()); err != nil {
			t.Fatalf("disjoint segments must both fit: %v", err)
		}
	}
	if err := db.DecideBooking(ctx, driver, bc, true, time.Now()); !errors.Is(err, ride.ErrInsufficientSeats) {
		t.Fatalf("C over both segments: err = %v", err)
	}
	if got := segmentLoad(t, db, trip); got[0] != 2 || got[1] != 3 {
		t.Fatalf("load = %v, want [2 3]", got)
	}
	// One seat is free on the whole route.
	bd := mustBook(t, db, trip, pd, 0, 2, 1)
	if err := db.DecideBooking(ctx, driver, bd, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if s := tripStatus(t, db, trip); s != "active" {
		t.Fatalf("first segment still has a seat, status = %s", s)
	}
	got, err := db.Trip(ctx, trip, pa)
	if err != nil {
		t.Fatal(err)
	}
	if got.FreeBySegment[0] != 1 || got.FreeBySegment[1] != 0 {
		t.Fatalf("free by segment = %v", got.FreeBySegment)
	}
	// Invalid segments are refused before any seat check.
	pe, _ := mkUser(t, db, "E")
	for _, seg := range [][2]int{{2, 0}, {1, 1}, {0, 3}} {
		if _, err := book(t, db, trip, pe, seg[0], seg[1], 1); !errors.Is(err, ride.ErrInvalidSegment) {
			t.Fatalf("segment %v: err = %v", seg, err)
		}
	}
	// A request that cannot fit any more is refused at creation.
	if _, err := book(t, db, trip, pe, 1, 2, 1); !errors.Is(err, ride.ErrInsufficientSeats) {
		t.Fatalf("full segment request: err = %v", err)
	}
}

func TestCancellationRestoresSeatsExactlyOnce(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	driver, _ := mkUser(t, db, "Driver")
	trip := mkTrip(t, db, driver, tripOpts{stops: []string{ashgabat, mary}})
	p, _ := mkUser(t, db, "P")
	q, _ := mkUser(t, db, "Q")
	b := mustBook(t, db, trip, p, 0, 1, 4)
	if err := db.DecideBooking(ctx, driver, b, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if s := tripStatus(t, db, trip); s != "full" {
		t.Fatalf("status = %s, want full", s)
	}
	// Duplicate approval is idempotent.
	if err := db.DecideBooking(ctx, driver, b, true, time.Now()); err != nil {
		t.Fatalf("repeated approve: %v", err)
	}
	if n := countNotes(t, db, p, "booking_approved"); n != 1 {
		t.Fatalf("approval notifications = %d", n)
	}

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := db.CancelBooking(ctx, p, b, time.Now()); err != nil {
				t.Errorf("cancel: %v", err)
			}
		}()
	}
	wg.Wait()
	if s := bookingStatus(t, db, b); s != "cancelled_by_passenger" {
		t.Fatalf("booking status = %s", s)
	}
	if load := segmentLoad(t, db, trip); load[0] != 0 {
		t.Fatalf("load after cancel = %v", load)
	}
	if s := tripStatus(t, db, trip); s != "active" {
		t.Fatalf("trip reopened status = %s", s)
	}
	if n := countNotes(t, db, driver, "booking_cancelled_by_passenger"); n != 1 {
		t.Fatalf("cancel notifications = %d, want 1", n)
	}
	// A cancelled booking cannot silently become approved.
	if err := db.DecideBooking(ctx, driver, b, true, time.Now()); !errors.Is(err, ride.ErrBookingNotPending) {
		t.Fatalf("approve cancelled: err = %v", err)
	}
	// The restored seats can be taken by someone else, exactly 4 and not 8.
	bq := mustBook(t, db, trip, q, 0, 1, 4)
	if err := db.DecideBooking(ctx, driver, bq, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if load := segmentLoad(t, db, trip); load[0] != 4 {
		t.Fatalf("load = %v", load)
	}
	// A rejected request cannot be approved later.
	r, _ := mkUser(t, db, "R")
	if _, err := book(t, db, trip, r, 0, 1, 1); !errors.Is(err, ride.ErrInsufficientSeats) {
		t.Fatalf("full trip request: err = %v", err)
	}
}

func TestDriverCancelsApprovedBookingAndTrip(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	driver, _ := mkUser(t, db, "Driver")
	trip := mkTrip(t, db, driver, tripOpts{})
	p1, _ := mkUser(t, db, "P1")
	p2, _ := mkUser(t, db, "P2")
	stranger, _ := mkUser(t, db, "S")
	b1 := mustBook(t, db, trip, p1, 0, 2, 2)
	b2 := mustBook(t, db, trip, p2, 1, 2, 1)
	if err := db.DecideBooking(ctx, driver, b1, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.CancelBooking(ctx, stranger, b1, time.Now()); !errors.Is(err, ride.ErrNotFound) {
		t.Fatalf("stranger cancel: err = %v", err)
	}
	if err := db.DecideBooking(ctx, p2, b2, true, time.Now()); !errors.Is(err, ride.ErrForbidden) {
		t.Fatalf("passenger approving own request: err = %v", err)
	}
	if err := db.CancelBooking(ctx, driver, b1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if s := bookingStatus(t, db, b1); s != "cancelled_by_driver" {
		t.Fatalf("status = %s", s)
	}
	if err := db.CancelTrip(ctx, stranger, trip, time.Now()); !errors.Is(err, ride.ErrForbidden) {
		t.Fatalf("stranger cancels trip: err = %v", err)
	}
	for range 2 {
		if err := db.CancelTrip(ctx, driver, trip, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if s := bookingStatus(t, db, b2); s != "cancelled_by_driver" {
		t.Fatalf("pending booking after trip cancel = %s", s)
	}
	if n := countNotes(t, db, p2, "trip_cancelled"); n != 1 {
		t.Fatalf("trip cancelled notifications = %d", n)
	}
	if _, err := book(t, db, trip, stranger, 0, 1, 1); !errors.Is(err, ride.ErrTripNotActive) {
		t.Fatalf("booking a cancelled trip: err = %v", err)
	}
}

func TestDriverCannotPublishOverlappingTrips(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	driver, _ := mkUser(t, db, "Driver")
	vehicle := mkVehicle(t, db, driver, 4)
	base := time.Now().Add(72 * time.Hour).Truncate(time.Minute)
	input := func(dep time.Time, hours int) ride.TripInput {
		in := ride.TripInput{
			DriverID: driver, VehicleID: vehicle, DepartsAt: dep, ArrivesAt: dep.Add(time.Duration(hours) * time.Hour),
			SeatsTotal: 3, Stops: []ride.Stop{{LocationID: ashgabat}, {LocationID: mary}},
		}
		if err := in.Normalize(time.Now()); err != nil {
			t.Fatal(err)
		}
		return in
	}

	// Ten simultaneous publications of overlapping trips: one wins.
	var wg sync.WaitGroup
	var mu sync.Mutex
	var created []string
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := db.CreateTrip(ctx, input(base.Add(time.Duration(i)*10*time.Minute), 6), 30*time.Minute)
			if err == nil {
				mu.Lock()
				created = append(created, id)
				mu.Unlock()
			} else if !errors.Is(err, ride.ErrTripOverlap) {
				t.Errorf("create: %v", err)
			}
		}()
	}
	wg.Wait()
	if len(created) != 1 {
		t.Fatalf("created %d overlapping trips, want 1", len(created))
	}
	var live int
	if err := db.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM trips WHERE driver_id = $1::uuid AND status IN ('active', 'full', 'in_progress')
	`, driver).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Fatalf("live trips in the database = %d", live)
	}

	var first time.Time
	if err := db.pool.QueryRow(ctx, `SELECT arrives_at FROM trips WHERE id = $1::uuid`, created[0]).Scan(&first); err != nil {
		t.Fatal(err)
	}
	// Inside the safety buffer after arrival: refused, and the error names the trip.
	_, err := db.CreateTrip(ctx, input(first.Add(20*time.Minute), 2), 30*time.Minute)
	var overlap *ride.OverlapError
	if !errors.As(err, &overlap) || overlap.TripID != created[0] {
		t.Fatalf("buffer overlap: err = %v", err)
	}
	// After the buffer: allowed.
	if _, err := db.CreateTrip(ctx, input(first.Add(40*time.Minute), 2), 30*time.Minute); err != nil {
		t.Fatalf("after buffer: %v", err)
	}
	// A different driver may drive at the same time.
	other, _ := mkUser(t, db, "Other")
	otherIn := input(base, 6)
	otherIn.DriverID, otherIn.VehicleID = other, mkVehicle(t, db, other, 4)
	if _, err := db.CreateTrip(ctx, otherIn, 30*time.Minute); err != nil {
		t.Fatalf("other driver: %v", err)
	}
	// A cancelled trip no longer blocks the slot.
	if err := db.CancelTrip(ctx, driver, created[0], time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateTrip(ctx, input(base, 6), 30*time.Minute); err != nil {
		t.Fatalf("slot of a cancelled trip: %v", err)
	}
	// The database constraint itself refuses an overlap that skips the service check.
	_, err = db.pool.Exec(ctx, `
		INSERT INTO trips (driver_id, from_location_id, to_location_id, departs_at, arrives_at, seats_total)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, 2)
	`, driver, ashgabat, mary, base.Add(time.Hour), base.Add(3*time.Hour))
	if !isExclusion(err) {
		t.Fatalf("exclusion constraint: err = %v", err)
	}
}

func TestContactAndPlateArePrivateUntilApproval(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	driver, driverPhone := mkUser(t, db, "Driver")
	passenger, passengerPhone := mkUser(t, db, "Passenger")
	stranger, _ := mkUser(t, db, "Stranger")
	trip := mkTrip(t, db, driver, tripOpts{})
	b := mustBook(t, db, trip, passenger, 0, 2, 1)

	for _, viewer := range []string{passenger, driver} {
		got, err := db.Booking(ctx, viewer, b)
		if err != nil {
			t.Fatal(err)
		}
		if got.ContactPhone != "" {
			t.Fatalf("pending booking leaked a phone to %s", viewer)
		}
		if _, err := db.BookingContact(ctx, viewer, b); !errors.Is(err, ride.ErrContactUnavailable) {
			t.Fatalf("pending contact err = %v", err)
		}
	}
	if _, err := db.Booking(ctx, stranger, b); !errors.Is(err, ride.ErrNotFound) {
		t.Fatalf("stranger booking err = %v", err)
	}
	pending, err := db.Trip(ctx, trip, passenger)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Vehicle.Plate != "" {
		t.Fatal("plate visible before approval")
	}

	if err := db.DecideBooking(ctx, driver, b, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if phone, err := db.BookingContact(ctx, passenger, b); err != nil || phone != driverPhone {
		t.Fatalf("passenger sees %q err=%v, want driver phone", phone, err)
	}
	if phone, err := db.BookingContact(ctx, driver, b); err != nil || phone != passengerPhone {
		t.Fatalf("driver sees %q err=%v, want passenger phone", phone, err)
	}
	if _, err := db.BookingContact(ctx, stranger, b); !errors.Is(err, ride.ErrNotFound) {
		t.Fatalf("stranger contact err = %v", err)
	}
	approved, err := db.Trip(ctx, trip, passenger)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Vehicle.Plate == "" {
		t.Fatal("approved passenger cannot see the plate")
	}
	other, err := db.Trip(ctx, trip, stranger)
	if err != nil {
		t.Fatal(err)
	}
	if other.Vehicle.Plate != "" {
		t.Fatal("stranger sees the plate")
	}
	if pub, err := db.PublicProfile(ctx, driver); err != nil || pub.Phone != "" {
		t.Fatalf("public profile phone = %q err=%v", pub.Phone, err)
	}
}

func departTripInPast(t *testing.T, db *AppDB, tripID string) {
	t.Helper()
	// The overlap constraint does not care: the driver has only this trip in that window.
	if _, err := db.pool.Exec(context.Background(), `
		UPDATE trips SET departs_at = now() - interval '7 hours', arrives_at = now() - interval '1 hour' WHERE id = $1::uuid
	`, tripID); err != nil {
		t.Fatal(err)
	}
}

func TestReviewsNeedACompletedBooking(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	driver, _ := mkUser(t, db, "Driver")
	passenger, _ := mkUser(t, db, "Passenger")
	stranger, _ := mkUser(t, db, "Stranger")
	trip := mkTrip(t, db, driver, tripOpts{})
	b := mustBook(t, db, trip, passenger, 0, 2, 1)
	rejectedUser, _ := mkUser(t, db, "Rejected")
	rb := mustBook(t, db, trip, rejectedUser, 0, 1, 1)
	if err := db.DecideBooking(ctx, driver, rb, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	review := func(booking, author string, rating int) error {
		_, err := db.CreateReview(ctx, ride.ReviewInput{BookingID: booking, AuthorID: author, Rating: rating})
		return err
	}
	if err := review(b, passenger, 5); !errors.Is(err, ride.ErrReviewNotAllowed) {
		t.Fatalf("pending booking review err = %v", err)
	}
	if err := db.DecideBooking(ctx, driver, b, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := review(b, passenger, 5); !errors.Is(err, ride.ErrReviewNotAllowed) {
		t.Fatalf("not completed review err = %v", err)
	}
	if err := db.CompleteTrip(ctx, driver, trip, time.Now()); !errors.Is(err, ride.ErrTripNotStarted) {
		t.Fatalf("complete before departure err = %v", err)
	}
	departTripInPast(t, db, trip)
	if err := db.CompleteTrip(ctx, driver, trip, time.Now()); err != nil {
		t.Fatal(err)
	}
	if s := bookingStatus(t, db, b); s != "completed" {
		t.Fatalf("booking status = %s", s)
	}
	if err := db.CancelBooking(ctx, passenger, b, time.Now()); !errors.Is(err, ride.ErrInvalidTransition) {
		t.Fatalf("cancel a completed booking: err = %v", err)
	}
	if err := review(b, stranger, 1); !errors.Is(err, ride.ErrNotFound) {
		t.Fatalf("stranger review err = %v", err)
	}
	if err := review(rb, rejectedUser, 1); !errors.Is(err, ride.ErrReviewNotAllowed) {
		t.Fatalf("rejected booking review err = %v", err)
	}
	if err := review(b, passenger, 4); err != nil {
		t.Fatal(err)
	}
	if err := review(b, passenger, 1); !errors.Is(err, ride.ErrReviewExists) {
		t.Fatalf("duplicate review err = %v", err)
	}
	if err := review(b, driver, 5); err != nil {
		t.Fatalf("driver rates passenger: %v", err)
	}
	p, err := db.PublicProfile(ctx, driver)
	if err != nil {
		t.Fatal(err)
	}
	if p.RatingCount != 1 || p.RatingAvg != 4 || p.TripsAsDriver != 1 {
		t.Fatalf("driver rating = %.2f x%d trips=%d", p.RatingAvg, p.RatingCount, p.TripsAsDriver)
	}
	reviews, _, err := db.UserReviews(ctx, passenger, 1, 10)
	if err != nil || len(reviews) != 1 || reviews[0].AuthorRole != "driver" {
		t.Fatalf("passenger reviews = %+v err=%v", reviews, err)
	}
}

func TestSearchMatchesSegmentsAndWelayats(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	driver, _ := mkUser(t, db, "Driver")
	viewer, _ := mkUser(t, db, "Viewer")
	departs := time.Now().Add(96 * time.Hour).Truncate(time.Minute)
	trip := mkTrip(t, db, driver, tripOpts{departs: departs, seats: 3})
	date := departs.In(time.FixedZone("UTC+5", 5*3600))
	day := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)

	find := func(q ride.SearchQuery) *ride.Trip {
		t.Helper()
		q.ViewerID = viewer
		q.Date = &day
		if err := q.Normalize(); err != nil {
			t.Fatal(err)
		}
		q.Limit = 50
		list, _, err := db.SearchTrips(ctx, q, time.Now(), "Asia/Ashgabat")
		if err != nil {
			t.Fatal(err)
		}
		for i := range list {
			if list[i].ID == trip {
				return &list[i]
			}
		}
		return nil
	}

	// Ahal welayat contains Tejen, an intermediate stop.
	got := find(ride.SearchQuery{FromID: ahal, ToID: mary})
	if got == nil || got.Match.PickupOrder != 1 || got.Match.DropoffOrder != 2 || got.Match.FreeSeats != 3 {
		t.Fatalf("welayat search = %+v", got)
	}
	if find(ride.SearchQuery{FromID: mary, ToID: ashgabat}) != nil {
		t.Fatal("reverse direction must not match")
	}
	if find(ride.SearchQuery{FromID: ashgabat, ToID: tejen, Seats: 4}) != nil {
		t.Fatal("4 seats on a 3-seat trip must not match")
	}

	p, _ := mkUser(t, db, "P")
	b := mustBook(t, db, trip, p, 1, 2, 3)
	if err := db.DecideBooking(ctx, driver, b, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if find(ride.SearchQuery{FromID: tejen, ToID: mary}) != nil {
		t.Fatal("full Tejen -> Mary segment must not match")
	}
	if got := find(ride.SearchQuery{FromID: ashgabat, ToID: tejen}); got == nil || got.Match.FreeSeats != 3 {
		t.Fatalf("free Ashgabat -> Tejen segment = %+v", got)
	}
	// The driver does not find their own trip.
	q := ride.SearchQuery{ViewerID: driver, FromID: ashgabat, ToID: tejen}
	_ = q.Normalize()
	own, _, err := db.SearchTrips(ctx, q, time.Now(), "Asia/Ashgabat")
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range own {
		if tr.ID == trip {
			t.Fatal("driver found their own trip")
		}
	}
}

func TestLifecycleStartsAndCompletesTrips(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	driver, _ := mkUser(t, db, "Driver")
	trip := mkTrip(t, db, driver, tripOpts{})
	p, _ := mkUser(t, db, "P")
	q, _ := mkUser(t, db, "Q")
	approved := mustBook(t, db, trip, p, 0, 2, 1)
	pending := mustBook(t, db, trip, q, 0, 2, 1)
	if err := db.DecideBooking(ctx, driver, approved, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `
		UPDATE trips SET departs_at = now() - interval '1 hour', arrives_at = now() + interval '4 hours' WHERE id = $1::uuid
	`, trip); err != nil {
		t.Fatal(err)
	}
	if err := db.TripLifecycle(ctx, time.Now(), 6*time.Hour, 2*time.Hour); err != nil {
		t.Fatal(err)
	}
	if s := tripStatus(t, db, trip); s != "in_progress" {
		t.Fatalf("status = %s, want in_progress", s)
	}
	if s := bookingStatus(t, db, pending); s != "expired" {
		t.Fatalf("pending booking = %s, want expired", s)
	}
	if err := db.TripLifecycle(ctx, time.Now().Add(11*time.Hour), 6*time.Hour, 2*time.Hour); err != nil {
		t.Fatal(err)
	}
	if s := tripStatus(t, db, trip); s != "completed" {
		t.Fatalf("status = %s, want completed", s)
	}
	if s := bookingStatus(t, db, approved); s != "completed" {
		t.Fatalf("approved booking = %s, want completed", s)
	}
}

func TestPhoneChangeRequiresTheNewNumbersCode(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	user, _ := mkUser(t, db, "U")
	_, taken := mkUser(t, db, "Taken")
	newPhone := randomPhone()
	const secret = "integration-test-otp-secret"
	issue := func(phone, code string) {
		t.Helper()
		if _, err := db.CreateChallenge(ctx, auth.Challenge{
			Phone: phone, CodeHash: auth.HashOTP(secret, code), Message: "test", ExpiresAt: time.Now().Add(5 * time.Minute),
			Now: time.Now(), SkipLimits: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	spec := func(phone, code string) auth.VerifySpec {
		return auth.VerifySpec{Phone: phone, Now: time.Now(), MaxAttempts: 5,
			Match: func(s string) bool { return auth.OTPEqual(secret, code, s) }}
	}
	issue(newPhone, "123456")
	if _, err := db.ChangePhone(ctx, user, spec(newPhone, "000000")); !errors.Is(err, auth.ErrInvalidCode) {
		t.Fatalf("wrong code err = %v", err)
	}
	p, err := db.ChangePhone(ctx, user, spec(newPhone, "123456"))
	if err != nil || p.Phone != newPhone {
		t.Fatalf("change = %+v err=%v", p.Phone, err)
	}
	if _, err := db.ChangePhone(ctx, user, spec(newPhone, "123456")); !errors.Is(err, auth.ErrInvalidCode) {
		t.Fatalf("replayed code err = %v", err)
	}
	issue(taken, "654321")
	if _, err := db.ChangePhone(ctx, user, spec(taken, "654321")); !errors.Is(err, ride.ErrPhoneTaken) {
		t.Fatalf("taken number err = %v", err)
	}
}
