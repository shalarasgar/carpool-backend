package activity

import (
	"sort"
	"time"
)

// Slot is one hour of the week: Weekday 1 (Monday) to 7 (Sunday), Hour 0–23, local time.
type Slot struct {
	Weekday int
	Hour    int
	// Avg is the mean number of users in this hour over the weeks of the period, counting
	// hours with nobody as zero; Max is the busiest single occurrence.
	Avg float64
	Max int
}

// Report is the activity view of the admin panel.
type Report struct {
	// From is local midnight of the first day asked for; To is now.
	From, To time.Time
	// TrackingSince is the first hour with any count, nil before anyone was counted.
	// Averages start there, so a new installation is not diluted by empty weeks.
	TrackingSince *time.Time
	// Every day from the first counted one (or From) to today; days with nobody are zero.
	Days []DayCount
	// The last 48 hours up to the current one, zeros included.
	Hours []HourCount
	// 7×24 slots, Monday 00:00 first.
	Heatmap []Slot
	// Up to five busiest slots by average, busiest first.
	Peaks []Slot
}

// HoursShown is how many recent hours Report.Hours holds.
const HoursShown = 48

// BuildReport turns stored counts into the report for the days from..now. hours and days
// may be in any order and may include buckets outside the period.
func BuildReport(hours []HourCount, days []DayCount, first *time.Time, from, now time.Time, loc *time.Location) Report {
	if loc == nil {
		loc = time.UTC
	}
	from = DayStart(from, loc)
	r := Report{From: from, To: now, TrackingSince: first}

	byHour := make(map[int64]int, len(hours))
	for _, h := range hours {
		byHour[HourStart(h.Start, loc).Unix()] = h.Users
	}
	byDay := make(map[int64]int, len(days))
	for _, d := range days {
		byDay[DayStart(d.Day, loc).Unix()] = d.Users
	}

	start := from
	if first != nil && HourStart(*first, loc).After(start) {
		start = HourStart(*first, loc)
	}
	current := HourStart(now, loc)

	// Days: dense from the first counted day to today.
	if first != nil {
		for d := DayStart(start, loc); !d.After(now); d = nextDay(d, loc) {
			r.Days = append(r.Days, DayCount{Day: d, Users: byDay[d.Unix()]})
		}
	}

	// The last 48 hours, the current one included.
	for i := HoursShown - 1; i >= 0; i-- {
		h := HourStart(current.Add(-time.Duration(i)*time.Hour), loc)
		r.Hours = append(r.Hours, HourCount{Start: h, Users: byHour[h.Unix()]})
	}

	// Heatmap over finished hours only: the current hour is still filling up.
	var sum [7][24]int
	var seen [7][24]int
	var peak [7][24]int
	if first != nil {
		for h := start; h.Before(current); h = HourStart(h.Add(time.Hour), loc) {
			wd, hr := weekday(h), h.Hour()
			n := byHour[h.Unix()]
			sum[wd][hr] += n
			seen[wd][hr]++
			peak[wd][hr] = max(peak[wd][hr], n)
		}
	}
	for wd := range 7 {
		for hr := range 24 {
			s := Slot{Weekday: wd + 1, Hour: hr, Max: peak[wd][hr]}
			if seen[wd][hr] > 0 {
				s.Avg = float64(sum[wd][hr]) / float64(seen[wd][hr])
			}
			r.Heatmap = append(r.Heatmap, s)
		}
	}

	for _, s := range r.Heatmap {
		if s.Avg > 0 {
			r.Peaks = append(r.Peaks, s)
		}
	}
	sort.SliceStable(r.Peaks, func(i, j int) bool { return r.Peaks[i].Avg > r.Peaks[j].Avg })
	if len(r.Peaks) > 5 {
		r.Peaks = r.Peaks[:5]
	}
	return r
}

// weekday is 0 for Monday through 6 for Sunday.
func weekday(t time.Time) int { return (int(t.Weekday()) + 6) % 7 }

func nextDay(d time.Time, loc *time.Location) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, loc)
}
