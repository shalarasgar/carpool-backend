package store

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/activity"
)

func TestActivityCountsKeepTheLargerValue(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	loc := time.FixedZone("Asia/Ashgabat", 5*3600)
	// A random day far in the past, so runs on the shared test database do not collide.
	day := time.Date(1990+rand.IntN(30), time.Month(1+rand.IntN(12)), 1+rand.IntN(27), 0, 0, 0, 0, loc)
	h9, h10 := day.Add(9*time.Hour), day.Add(10*time.Hour)

	save := func(hour time.Time, users int) {
		t.Helper()
		s := activity.NewCounter(loc)
		for i := range users {
			s.Seen(string(rune('a'+i)), hour)
		}
		if err := db.SaveActivity(ctx, s.Snapshot(hour)); err != nil {
			t.Fatal(err)
		}
	}
	save(h9, 3)
	save(h9, 2) // a restart counted the same hour again from zero: 3 stays
	save(h10, 4)

	hours, days, first, err := db.ActivityCounts(ctx, day, day, loc)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]int{}
	for _, h := range hours {
		got[h.Start.Unix()] = h.Users
	}
	if got[h9.Unix()] != 3 || got[h10.Unix()] != 4 {
		t.Fatalf("hours = %+v", hours)
	}
	var dayUsers int
	for _, d := range days {
		if d.Day.Equal(day) {
			dayUsers = d.Users
		}
	}
	// Each save counted its own users; the day keeps the largest single count.
	if dayUsers != 4 {
		t.Fatalf("days = %+v", days)
	}
	if first == nil || first.After(h9) {
		t.Fatalf("first = %v", first)
	}
	if err := db.SaveActivity(ctx, activity.Snapshot{}); err != nil {
		t.Fatalf("empty snapshot: %v", err)
	}
}
