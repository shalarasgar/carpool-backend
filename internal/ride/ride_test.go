package ride

import (
	"errors"
	"testing"
	"time"
)

const (
	locA = "c2000000-0000-4000-8000-000000000001"
	locB = "c2000000-0000-4000-8000-000000000003"
	locC = "c2000000-0000-4000-8000-000000000008"
)

// Ashgabat(0) -> Tejen(1) -> Mary(2), four passenger seats.
const capacity, stopCount = 4, 3

func TestSegmentsDoNotShareSeatsAcrossDisjointParts(t *testing.T) {
	t.Parallel()
	occ := []Occupant{{From: 0, To: 1, Seats: 2}} // A: Ashgabat -> Tejen
	if !Fits(capacity, occ, stopCount, 1, 2, 3) {
		t.Fatal("B (Tejen -> Mary, 3 seats) must fit next to A")
	}
	occ = append(occ, Occupant{From: 1, To: 2, Seats: 3})
	if got := FreeSeats(capacity, occ, stopCount, 0, 2); got != 1 {
		t.Fatalf("free on the whole route = %d, want 1", got)
	}
	if Fits(capacity, occ, stopCount, 0, 2, 3) {
		t.Fatal("C (Ashgabat -> Mary, 3 seats) overlaps both segments and must not fit")
	}
	if !Fits(capacity, occ, stopCount, 0, 1, 2) {
		t.Fatal("2 more seats fit on the first segment")
	}
}

func TestFullOnlyWhenEverySegmentIsFull(t *testing.T) {
	t.Parallel()
	occ := []Occupant{{From: 0, To: 1, Seats: 4}}
	if IsFull(capacity, occ, stopCount) {
		t.Fatal("second segment still has seats")
	}
	occ = append(occ, Occupant{From: 1, To: 2, Seats: 4})
	if !IsFull(capacity, occ, stopCount) {
		t.Fatal("both segments are full")
	}
	if got := StatusAfterLoad(TripFull, capacity, occ[:1], stopCount); got != TripActive {
		t.Fatalf("status after a cancellation = %s", got)
	}
	if got := StatusAfterLoad(TripCancelled, capacity, nil, stopCount); got != TripCancelled {
		t.Fatalf("terminal status changed to %s", got)
	}
}

func TestFitsRejectsZeroAndOversize(t *testing.T) {
	t.Parallel()
	if Fits(capacity, nil, stopCount, 0, 2, 0) || Fits(capacity, nil, stopCount, 0, 2, 5) {
		t.Fatal("zero or more than capacity must not fit")
	}
}

func TestBookingTransitions(t *testing.T) {
	t.Parallel()
	allowed := [][2]BookingStatus{
		{BookingPending, BookingApproved}, {BookingPending, BookingRejected},
		{BookingPending, BookingCancelledByPassenger}, {BookingPending, BookingExpired},
		{BookingApproved, BookingCancelledByDriver}, {BookingApproved, BookingCompleted},
	}
	for _, c := range allowed {
		if !CanTransitionBooking(c[0], c[1]) {
			t.Errorf("%s -> %s should be allowed", c[0], c[1])
		}
	}
	denied := [][2]BookingStatus{
		{BookingRejected, BookingApproved}, {BookingCancelledByPassenger, BookingApproved},
		{BookingCompleted, BookingCancelledByPassenger}, {BookingApproved, BookingRejected},
		{BookingExpired, BookingApproved}, {BookingApproved, BookingApproved},
	}
	for _, c := range denied {
		if CanTransitionBooking(c[0], c[1]) {
			t.Errorf("%s -> %s should be denied", c[0], c[1])
		}
	}
}

func TestTripTransitions(t *testing.T) {
	t.Parallel()
	if CanTransitionTrip(TripCompleted, TripCancelled) || CanTransitionTrip(TripCancelled, TripActive) {
		t.Fatal("terminal trips must not change")
	}
	if !CanTransitionTrip(TripFull, TripActive) || !CanTransitionTrip(TripInProgress, TripCompleted) {
		t.Fatal("expected transitions missing")
	}
	if CanTransitionTrip(TripInProgress, TripCancelled) {
		t.Fatal("a departed trip cannot be cancelled")
	}
}

func TestOverlapsUsesBuffer(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 11, 15, 8, 0, 0, 0, time.UTC)
	end := base.Add(6 * time.Hour) // 08:00-14:00
	if !Overlaps(base, end, base.Add(5*time.Hour), base.Add(8*time.Hour), 0) {
		t.Fatal("13:00 start overlaps")
	}
	if Overlaps(base, end, end, end.Add(time.Hour), 0) {
		t.Fatal("back-to-back trips do not overlap without a buffer")
	}
	if !Overlaps(base, end, end.Add(20*time.Minute), end.Add(2*time.Hour), 30*time.Minute) {
		t.Fatal("a start inside the buffer overlaps")
	}
	if Overlaps(base, end, end.Add(time.Hour), end.Add(3*time.Hour), 30*time.Minute) {
		t.Fatal("a start after the buffer does not overlap")
	}
}

func TestNormalizeRoute(t *testing.T) {
	t.Parallel()
	dep := time.Date(2026, 11, 15, 8, 0, 0, 0, time.UTC)
	arr := dep.Add(6 * time.Hour)
	mid := dep.Add(2 * time.Hour)
	stops, err := NormalizeRoute([]Stop{
		{LocationID: locA, MeetingPoint: "  Teke bazar "},
		{LocationID: locB, PlannedAt: &mid},
		{LocationID: locC},
	}, dep, arr)
	if err != nil {
		t.Fatal(err)
	}
	if stops[0].Type != StopPickup || stops[2].Type != StopDropoff || stops[1].Type != StopBoth {
		t.Fatalf("types = %s %s %s", stops[0].Type, stops[1].Type, stops[2].Type)
	}
	if stops[0].MeetingPoint != "Teke bazar" || !stops[2].PlannedAt.Equal(arr) || stops[1].Order != 1 {
		t.Fatalf("normalized = %+v", stops)
	}

	bad := []struct {
		name  string
		stops []Stop
	}{
		{"one stop", []Stop{{LocationID: locA}}},
		{"duplicate", []Stop{{LocationID: locA}, {LocationID: locB}, {LocationID: locA}}},
		{"dropoff first", []Stop{{LocationID: locA, Type: StopDropoff}, {LocationID: locB}}},
		{"pickup last", []Stop{{LocationID: locA}, {LocationID: locB, Type: StopPickup}}},
		{"time after arrival", []Stop{{LocationID: locA}, {LocationID: locB, PlannedAt: &arr}, {LocationID: locC}}},
	}
	for _, c := range bad {
		if _, err := NormalizeRoute(c.stops, dep, arr); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
}

func TestValidateSegment(t *testing.T) {
	t.Parallel()
	stops := []Stop{{Order: 0, Type: StopPickup}, {Order: 1, Type: StopDropoff}, {Order: 2, Type: StopDropoff}}
	if err := ValidateSegment(stops, 0, 2); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]int{{2, 0}, {1, 1}, {1, 2}, {0, 3}, {-1, 1}} {
		if err := ValidateSegment(stops, c[0], c[1]); !errors.Is(err, ErrInvalidSegment) {
			t.Errorf("%v: err = %v", c, err)
		}
	}
}

func TestTripInputValidation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	in := TripInput{
		VehicleID: locA, SeatsTotal: 4,
		DepartsAt: now.Add(24 * time.Hour), ArrivesAt: now.Add(30 * time.Hour),
		Stops:     []Stop{{LocationID: locA}, {LocationID: locB}},
		Amenities: Amenities{AirConditioning: "yes"},
	}
	if err := in.Normalize(now); err != nil {
		t.Fatal(err)
	}
	if in.Amenities.PetsPolicy != Unspecified {
		t.Fatalf("default amenity = %q", in.Amenities.PetsPolicy)
	}
	past := in
	past.DepartsAt = now.Add(-time.Minute)
	if err := past.Normalize(now); !errors.Is(err, ErrValidation) {
		t.Fatalf("past departure err = %v", err)
	}
	seats := in
	seats.SeatsTotal = 9
	if err := seats.Normalize(now); !errors.Is(err, ErrValidation) {
		t.Fatalf("seats err = %v", err)
	}
	am := in
	am.Amenities = Amenities{SmokingPolicy: "sometimes"}
	if err := am.Normalize(now); !errors.Is(err, ErrValidation) {
		t.Fatalf("amenity err = %v", err)
	}
}
