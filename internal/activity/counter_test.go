package activity

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

var ashgabat = time.FixedZone("Asia/Ashgabat", 5*3600)

func at(day, hour, minute int) time.Time {
	return time.Date(2026, 3, day, hour, minute, 0, 0, ashgabat)
}

func TestCounterCountsEachPersonOncePerHourAndDay(t *testing.T) {
	t.Parallel()
	c := NewCounter(ashgabat)
	c.Seen("a", at(2, 9, 5))
	c.Seen("a", at(2, 9, 40))
	c.Seen("b", at(2, 9, 41))
	c.Seen("", at(2, 9, 42)) // no user, not counted
	c.Seen("a", at(2, 10, 1))

	s := c.Snapshot(at(2, 10, 30))
	want := []HourCount{{Start: at(2, 9, 0), Users: 2}, {Start: at(2, 10, 0), Users: 1}}
	if len(s.Hours) != 2 || !s.Hours[0].Start.Equal(want[0].Start) || s.Hours[0].Users != 2 ||
		!s.Hours[1].Start.Equal(want[1].Start) || s.Hours[1].Users != 1 {
		t.Fatalf("hours = %+v", s.Hours)
	}
	if len(s.Days) != 1 || !s.Days[0].Day.Equal(at(2, 0, 0)) || s.Days[0].Users != 2 {
		t.Fatalf("days = %+v", s.Days)
	}
}

func TestCounterUsesTheLocalCalendarDay(t *testing.T) {
	t.Parallel()
	c := NewCounter(ashgabat)
	// 18:30 UTC on the 1st is 23:30 on the 1st in Ashgabat; 19:30 UTC is already the 2nd.
	c.Seen("a", time.Date(2026, 3, 1, 18, 30, 0, 0, time.UTC))
	c.Seen("a", time.Date(2026, 3, 1, 19, 30, 0, 0, time.UTC))

	s := c.Snapshot(time.Date(2026, 3, 1, 19, 31, 0, 0, time.UTC))
	if len(s.Days) != 2 || !s.Days[0].Day.Equal(at(1, 0, 0)) || !s.Days[1].Day.Equal(at(2, 0, 0)) {
		t.Fatalf("days = %+v", s.Days)
	}
	if !s.Hours[0].Start.Equal(at(1, 23, 0)) || !s.Hours[1].Start.Equal(at(2, 0, 0)) {
		t.Fatalf("hours = %+v", s.Hours)
	}
}

func TestCounterKeepsFinishedHoursUntilCommitted(t *testing.T) {
	t.Parallel()
	c := NewCounter(ashgabat)
	c.Seen("a", at(2, 9, 0))

	// The write after the hour ended fails: the finished hour must come back next time.
	failed := c.Snapshot(at(2, 10, 0))
	if len(failed.Hours) != 1 {
		t.Fatalf("first snapshot = %+v", failed.Hours)
	}
	c.Seen("b", at(2, 10, 5))
	retry := c.Snapshot(at(2, 10, 6))
	if len(retry.Hours) != 2 || retry.Hours[0].Users != 1 || retry.Hours[1].Users != 1 {
		t.Fatalf("retry = %+v", retry.Hours)
	}
	c.Commit(retry)
	after := c.Snapshot(at(2, 10, 7))
	if len(after.Hours) != 1 || !after.Hours[0].Start.Equal(at(2, 10, 0)) {
		t.Fatalf("after commit = %+v", after.Hours)
	}
	// An idle hour closes on its own; nothing to write when nobody came.
	if s := NewCounter(ashgabat).Snapshot(at(2, 11, 0)); !s.Empty() {
		t.Fatalf("empty counter = %+v", s)
	}
}

func TestCounterIsSafeForConcurrentRequests(t *testing.T) {
	t.Parallel()
	c := NewCounter(ashgabat)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 20 {
				c.Seen(fmt.Sprintf("u%d", (i*20+j)%100), at(2, 9, j))
			}
		}()
	}
	wg.Wait()
	if s := c.Snapshot(at(2, 9, 59)); s.Hours[0].Users != 100 || s.Days[0].Users != 100 {
		t.Fatalf("snapshot = %+v", s)
	}
}
