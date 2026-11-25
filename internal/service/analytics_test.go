package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/activity"
	"github.com/shalarasgar/carpool-backend/internal/ride"
)

type fakeActivityStore struct {
	saved      []activity.Snapshot
	fail       error
	hoursFrom  time.Time
	daysFrom   time.Time
	queriedLoc *time.Location
}

func (f *fakeActivityStore) SaveActivity(_ context.Context, s activity.Snapshot) error {
	if f.fail != nil {
		return f.fail
	}
	f.saved = append(f.saved, s)
	return nil
}

func (f *fakeActivityStore) ActivityCounts(_ context.Context, hoursFrom, daysFrom time.Time, loc *time.Location) (
	[]activity.HourCount, []activity.DayCount, *time.Time, error,
) {
	f.hoursFrom, f.daysFrom, f.queriedLoc = hoursFrom, daysFrom, loc
	return nil, nil, nil, nil
}

func TestActivityWritesCountsAndRetriesAFailedWrite(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("Asia/Ashgabat", 5*3600)
	store := &fakeActivityStore{}
	a := NewActivity(store, loc)
	clock := time.Date(2026, 3, 2, 9, 10, 0, 0, loc)
	a.now = func() time.Time { return clock }

	if err := a.Maintain(context.Background()); err != nil || len(store.saved) != 0 {
		t.Fatalf("nothing to write: err %v, saved %d", err, len(store.saved))
	}
	a.Seen("u1")
	a.Seen("u2")
	clock = clock.Add(time.Hour) // 10:10, the 09:00 hour is finished

	store.fail = errors.New("db down")
	if err := a.Maintain(context.Background()); err == nil {
		t.Fatal("a failed write must be reported")
	}
	store.fail = nil
	if err := a.Maintain(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := store.saved[0]
	if len(s.Hours) != 1 || s.Hours[0].Users != 2 || len(s.Days) != 1 || s.Days[0].Users != 2 {
		t.Fatalf("written after the retry = %+v", s)
	}
	if err := a.Maintain(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The finished hour was committed; the day is still running and written again.
	if again := store.saved[1]; len(again.Hours) != 0 || len(again.Days) != 1 {
		t.Fatalf("second write = %+v", again)
	}
}

func TestAnalyticsPeriod(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("Asia/Ashgabat", 5*3600)
	store := &fakeActivityStore{}
	a := NewAnalytics(store, loc)
	a.now = func() time.Time { return time.Date(2026, 3, 10, 21, 30, 0, 0, time.UTC) } // 02:30 on the 11th locally

	for _, days := range []int{-1, 91} {
		var field *ride.FieldError
		if _, err := a.Activity(context.Background(), days); !errors.As(err, &field) || field.Field != "days" {
			t.Fatalf("days=%d err = %v", days, err)
		}
	}

	r, err := a.Activity(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 3, 5, 0, 0, 0, 0, loc); !r.From.Equal(want) || !store.daysFrom.Equal(want) {
		t.Fatalf("from = %v, store days from %v", r.From, store.daysFrom)
	}
	if store.queriedLoc != loc {
		t.Fatal("the store must read days in the report's zone")
	}

	// One day: the 48-hour strip reaches into yesterday.
	if _, err := a.Activity(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 3, 9, 3, 0, 0, 0, loc); !store.hoursFrom.Equal(want) {
		t.Fatalf("hours from = %v, want %v", store.hoursFrom, want)
	}
	if r, _ := a.Activity(context.Background(), 0); !r.From.Equal(time.Date(2026, 2, 12, 0, 0, 0, 0, loc)) {
		t.Fatalf("default period from = %v", r.From)
	}
}
