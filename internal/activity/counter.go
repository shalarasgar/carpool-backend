// Package activity counts how many different people use the app per hour and per day.
//
// Only counts leave this package. The user ids of the current hour and day are kept in
// memory just long enough to count each person once; they are never stored or logged.
package activity

import (
	"sync"
	"time"
)

// HourCount is the number of different users seen in the local hour starting at Start.
type HourCount struct {
	Start time.Time
	Users int
}

// DayCount is the number of different users seen on a local calendar day (midnight in
// the counter's time zone).
type DayCount struct {
	Day   time.Time
	Users int
}

// Counter collects distinct users per local hour and day. It is safe for concurrent use.
//
// A restart loses the in-memory sets, so a person seen both before and after a restart
// within the same hour or day is counted once on each side and the larger count wins
// (see the store's upsert). That undercounts slightly around a deploy, never overcounts.
type Counter struct {
	loc *time.Location

	mu        sync.Mutex
	hour      time.Time
	hourUsers map[string]struct{}
	day       time.Time
	dayUsers  map[string]struct{}
	// Finished buckets waiting to be written.
	doneHours []HourCount
	doneDays  []DayCount
}

// NewCounter counts hours and days in loc (UTC when nil).
func NewCounter(loc *time.Location) *Counter {
	if loc == nil {
		loc = time.UTC
	}
	return &Counter{loc: loc, hourUsers: map[string]struct{}{}, dayUsers: map[string]struct{}{}}
}

// HourStart is the start of the local hour that contains t.
func HourStart(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), 0, 0, 0, loc)
}

// DayStart is local midnight of the day that contains t.
func DayStart(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
}

// Seen records that userID made a request at now.
func (c *Counter) Seen(userID string, now time.Time) {
	if userID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.roll(now)
	c.hourUsers[userID] = struct{}{}
	c.dayUsers[userID] = struct{}{}
}

// roll closes the current hour and day when now has left them. Callers hold mu.
func (c *Counter) roll(now time.Time) {
	if h := HourStart(now, c.loc); !h.Equal(c.hour) {
		if !c.hour.IsZero() && len(c.hourUsers) > 0 {
			c.doneHours = append(c.doneHours, HourCount{Start: c.hour, Users: len(c.hourUsers)})
		}
		c.hour, c.hourUsers = h, map[string]struct{}{}
	}
	if d := DayStart(now, c.loc); !d.Equal(c.day) {
		if !c.day.IsZero() && len(c.dayUsers) > 0 {
			c.doneDays = append(c.doneDays, DayCount{Day: c.day, Users: len(c.dayUsers)})
		}
		c.day, c.dayUsers = d, map[string]struct{}{}
	}
}

// Snapshot is what a flush writes: the finished buckets and the running ones.
type Snapshot struct {
	Hours []HourCount
	Days  []DayCount
	// How many of Hours and Days are finished; Commit drops them after a successful write.
	doneHours, doneDays int
}

// Empty reports whether there is nothing to write.
func (s Snapshot) Empty() bool { return len(s.Hours) == 0 && len(s.Days) == 0 }

// Snapshot returns the counts to write at now. Finished buckets stay queued until Commit,
// so a failed write is retried on the next flush.
func (c *Counter) Snapshot(now time.Time) Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.roll(now)
	s := Snapshot{
		Hours:     append([]HourCount(nil), c.doneHours...),
		Days:      append([]DayCount(nil), c.doneDays...),
		doneHours: len(c.doneHours),
		doneDays:  len(c.doneDays),
	}
	if len(c.hourUsers) > 0 {
		s.Hours = append(s.Hours, HourCount{Start: c.hour, Users: len(c.hourUsers)})
	}
	if len(c.dayUsers) > 0 {
		s.Days = append(s.Days, DayCount{Day: c.day, Users: len(c.dayUsers)})
	}
	return s
}

// Commit drops the finished buckets of a snapshot that was written.
func (c *Counter) Commit(s Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.doneHours = c.doneHours[min(s.doneHours, len(c.doneHours)):]
	c.doneDays = c.doneDays[min(s.doneDays, len(c.doneDays)):]
}
