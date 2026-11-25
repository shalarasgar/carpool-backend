package activity

import "testing"

func slot(r Report, weekday, hour int) Slot {
	return r.Heatmap[(weekday-1)*24+hour]
}

func TestReportAveragesEachHourOfTheWeekCountingQuietHoursAsZero(t *testing.T) {
	t.Parallel()
	// 2026-03-02 is a Monday. Two Mondays at 09:00: 4 users, then nobody.
	first := at(2, 9, 0)
	hours := []HourCount{
		{Start: at(2, 9, 0), Users: 4},
		{Start: at(2, 20, 0), Users: 6},
		{Start: at(3, 20, 0), Users: 2},
		{Start: at(16, 9, 0), Users: 3}, // the current hour: still filling, left out
	}
	now := at(16, 9, 30)
	r := BuildReport(hours, nil, &first, at(1, 0, 0), now, ashgabat)

	if len(r.Heatmap) != 7*24 {
		t.Fatalf("heatmap has %d slots", len(r.Heatmap))
	}
	// Mondays 09:00 in range: 2nd and 9th (the 16th's is the current hour).
	if s := slot(r, 1, 9); s.Avg != 2 || s.Max != 4 {
		t.Fatalf("Monday 09:00 = %+v", s)
	}
	if s := slot(r, 1, 20); s.Avg != 3 || s.Max != 6 {
		t.Fatalf("Monday 20:00 = %+v", s)
	}
	if s := slot(r, 2, 20); s.Avg != 1 {
		t.Fatalf("Tuesday 20:00 = %+v", s)
	}
	// Sunday 08:00 before tracking began on the 1st does not count; later ones are zeros.
	if s := slot(r, 7, 8); s.Avg != 0 || s.Max != 0 {
		t.Fatalf("Sunday 08:00 = %+v", s)
	}
	if len(r.Peaks) != 3 || r.Peaks[0].Weekday != 1 || r.Peaks[0].Hour != 20 || r.Peaks[1].Hour != 9 {
		t.Fatalf("peaks = %+v", r.Peaks)
	}
}

func TestReportDaysAndRecentHoursAreDense(t *testing.T) {
	t.Parallel()
	first := at(10, 14, 0)
	days := []DayCount{{Day: at(10, 0, 0), Users: 5}, {Day: at(12, 0, 0), Users: 1}}
	hours := []HourCount{{Start: at(12, 13, 0), Users: 2}}
	now := at(12, 14, 10)
	// Asking for a month: days start where counting started, not 30 days ago.
	r := BuildReport(hours, days, &first, at(1, 0, 0), now, ashgabat)

	if len(r.Days) != 3 || r.Days[0].Users != 5 || r.Days[1].Users != 0 || r.Days[2].Users != 1 {
		t.Fatalf("days = %+v", r.Days)
	}
	if len(r.Hours) != HoursShown || !r.Hours[HoursShown-1].Start.Equal(at(12, 14, 0)) ||
		r.Hours[HoursShown-2].Users != 2 {
		t.Fatalf("hours: %d, last %+v, before %+v", len(r.Hours), r.Hours[HoursShown-1], r.Hours[HoursShown-2])
	}
}

func TestReportBeforeAnyoneWasCounted(t *testing.T) {
	t.Parallel()
	r := BuildReport(nil, nil, nil, at(1, 0, 0), at(5, 12, 0), ashgabat)
	if r.TrackingSince != nil || len(r.Days) != 0 || len(r.Peaks) != 0 || len(r.Hours) != HoursShown {
		t.Fatalf("report = %+v", r)
	}
	for _, s := range r.Heatmap {
		if s.Avg != 0 || s.Max != 0 {
			t.Fatalf("slot = %+v", s)
		}
	}
}
