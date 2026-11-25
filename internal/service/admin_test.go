package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// fakeAdmin records what reaches the store.
type fakeAdmin struct {
	AdminStore
	since   time.Time
	queries []ride.UserQuery
	limits  []int
	userIDs []string
}

func (f *fakeAdmin) Overview(_ context.Context, since time.Time) (ride.Overview, error) {
	f.since = since
	return ride.Overview{ReportsOpen: 3}, nil
}

func (f *fakeAdmin) AdminUsers(_ context.Context, q ride.UserQuery, _, limit int) ([]ride.AdminUser, bool, error) {
	f.queries, f.limits = append(f.queries, q), append(f.limits, limit)
	return nil, false, nil
}

func (f *fakeAdmin) AdminUser(_ context.Context, id string) (ride.AdminUserDetail, error) {
	f.userIDs = append(f.userIDs, id)
	return ride.AdminUserDetail{}, nil
}

func (f *fakeAdmin) DriverTrips(_ context.Context, id, _ string, _, _ int) ([]ride.Trip, bool, error) {
	f.userIDs = append(f.userIDs, id)
	return nil, false, nil
}

func (f *fakeAdmin) PassengerBookings(_ context.Context, id, _ string, _, _ int) ([]ride.Booking, bool, error) {
	f.userIDs = append(f.userIDs, id)
	return nil, false, nil
}

func (f *fakeAdmin) UserReviews(_ context.Context, id string, _, _ int) ([]ride.Review, bool, error) {
	f.userIDs = append(f.userIDs, id)
	return nil, false, nil
}

func TestAdminOverviewCountsFromLocalMidnight(t *testing.T) {
	t.Parallel()
	store := &fakeAdmin{}
	loc := time.FixedZone("Asia/Ashgabat", 5*3600)
	a := NewAdmin(store, loc)
	a.now = func() time.Time { return time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC) } // 01:00 on the 2nd locally

	o, err := a.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	if !store.since.Equal(want) || !o.Since.Equal(want) || o.ReportsOpen != 3 {
		t.Fatalf("since = %v, overview = %+v", store.since, o)
	}
}

func TestAdminUsersValidatesAndBoundsThePage(t *testing.T) {
	t.Parallel()
	store := &fakeAdmin{}
	a := NewAdmin(store, nil)
	ctx := context.Background()

	if _, _, err := a.Users(ctx, ride.UserQuery{State: "banned"}, 1, 20); !errors.Is(err, ride.ErrValidation) {
		t.Fatalf("unknown state err = %v", err)
	}
	if _, _, err := a.Users(ctx, ride.UserQuery{Text: " +993 65 "}, 0, 5000); err != nil {
		t.Fatal(err)
	}
	if len(store.queries) != 1 || store.queries[0].Text != "+993 65" || store.queries[0].Digits != "99365" {
		t.Fatalf("queries = %+v", store.queries)
	}
	if store.limits[0] != 20 {
		t.Fatalf("limit = %d", store.limits[0])
	}
}

func TestAdminUserPagesRejectMalformedIDs(t *testing.T) {
	t.Parallel()
	store := &fakeAdmin{}
	a := NewAdmin(store, nil)
	ctx := context.Background()

	if _, err := a.User(ctx, "nope"); !errors.Is(err, ride.ErrNotFound) {
		t.Fatalf("user err = %v", err)
	}
	if _, _, err := a.UserTrips(ctx, "nope", "", 1, 20); !errors.Is(err, ride.ErrNotFound) {
		t.Fatalf("trips err = %v", err)
	}
	if _, _, err := a.UserBookings(ctx, "nope", "", 1, 20); !errors.Is(err, ride.ErrNotFound) {
		t.Fatalf("bookings err = %v", err)
	}
	if _, _, err := a.UserReviews(ctx, "nope", 1, 20); !errors.Is(err, ride.ErrNotFound) {
		t.Fatalf("reviews err = %v", err)
	}
	if len(store.userIDs) != 0 {
		t.Fatalf("a malformed id reached the store: %v", store.userIDs)
	}

	id := "00000000-0000-4000-8000-000000000001"
	_, _ = a.User(ctx, id)
	_, _, _ = a.UserTrips(ctx, id, "history", 1, 20)
	_, _, _ = a.UserBookings(ctx, id, "", 1, 20)
	_, _, _ = a.UserReviews(ctx, id, 1, 20)
	if len(store.userIDs) != 4 {
		t.Fatalf("calls = %v", store.userIDs)
	}
}
