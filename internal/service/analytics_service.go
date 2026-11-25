package service

import (
	"context"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/activity"
	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// ActivityStore keeps the hourly and daily user counts.
type ActivityStore interface {
	SaveActivity(ctx context.Context, s activity.Snapshot) error
	ActivityCounts(ctx context.Context, hoursFrom, daysFrom time.Time, loc *time.Location) (
		[]activity.HourCount, []activity.DayCount, *time.Time, error)
}

// Activity counts the users of authenticated requests and writes the counts on every
// Maintain (run by a jobs.Reaper), and once more on shutdown.
type Activity struct {
	counter *activity.Counter
	store   ActivityStore
	now     func() time.Time
}

// NewActivity counts hours and days in loc (UTC when nil).
func NewActivity(store ActivityStore, loc *time.Location) *Activity {
	return &Activity{counter: activity.NewCounter(loc), store: store, now: time.Now}
}

// Seen records a request by userID. It only touches memory.
func (a *Activity) Seen(userID string) { a.counter.Seen(userID, a.now()) }

// Maintain writes the counts. A failed write keeps finished hours for the next try.
func (a *Activity) Maintain(ctx context.Context) error {
	s := a.counter.Snapshot(a.now())
	if s.Empty() {
		return nil
	}
	if err := a.store.SaveActivity(ctx, s); err != nil {
		return err
	}
	a.counter.Commit(s)
	return nil
}

// Activity report periods, in days.
const (
	MinActivityDays     = 1
	MaxActivityDays     = 90
	DefaultActivityDays = 28
)

// Analytics builds the admin panel's activity report.
type Analytics struct {
	store ActivityStore
	loc   *time.Location
	now   func() time.Time
}

// NewAnalytics reports in loc (UTC when nil).
func NewAnalytics(store ActivityStore, loc *time.Location) *Analytics {
	if loc == nil {
		loc = time.UTC
	}
	return &Analytics{store: store, loc: loc, now: time.Now}
}

// Location is the time zone of the report.
func (a *Analytics) Location() *time.Location { return a.loc }

// Activity covers the last days calendar days, today included. 0 means the default.
func (a *Analytics) Activity(ctx context.Context, days int) (activity.Report, error) {
	if days == 0 {
		days = DefaultActivityDays
	}
	if days < MinActivityDays || days > MaxActivityDays {
		return activity.Report{}, &ride.FieldError{Field: "days", Reason: "range"}
	}
	now := a.now()
	today := activity.DayStart(now, a.loc)
	from := time.Date(today.Year(), today.Month(), today.Day()-(days-1), 0, 0, 0, 0, a.loc)
	// The 48-hour strip can reach before the period when it is one day long.
	hoursFrom := from
	if strip := activity.HourStart(now, a.loc).Add(-(activity.HoursShown - 1) * time.Hour); strip.Before(hoursFrom) {
		hoursFrom = strip
	}
	hours, dayCounts, first, err := a.store.ActivityCounts(ctx, hoursFrom, from, a.loc)
	if err != nil {
		return activity.Report{}, err
	}
	return activity.BuildReport(hours, dayCounts, first, from, now, a.loc), nil
}
