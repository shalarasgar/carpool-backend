package handlers

import (
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/activity"
	httperr "github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// Analytics serves the admin activity report. Only counts, never user ids.
type Analytics struct {
	Logger  *slog.Logger
	Service *service.Analytics
}

type dayCountDTO struct {
	Day   string `json:"day"`
	Users int    `json:"users"`
}

type hourCountDTO struct {
	HourStart time.Time `json:"hour_start"`
	Users     int       `json:"users"`
}

type slotDTO struct {
	Weekday int     `json:"weekday"`
	Hour    int     `json:"hour"`
	Avg     float64 `json:"avg"`
	Max     int     `json:"max"`
}

type activityDTO struct {
	TimeZone      string         `json:"timezone"`
	From          time.Time      `json:"from"`
	To            time.Time      `json:"to"`
	TrackingSince *time.Time     `json:"tracking_since"`
	Days          []dayCountDTO  `json:"days"`
	Hours         []hourCountDTO `json:"hours"`
	Heatmap       []slotDTO      `json:"heatmap"`
	Peaks         []slotDTO      `json:"peaks"`
}

func toSlots(in []activity.Slot) []slotDTO {
	out := make([]slotDTO, 0, len(in))
	for _, s := range in {
		out = append(out, slotDTO{Weekday: s.Weekday, Hour: s.Hour, Avg: math.Round(s.Avg*100) / 100, Max: s.Max})
	}
	return out
}

// Activity returns distinct users per hour and day, and the week heatmap.
func (h Analytics) Activity(w http.ResponseWriter, r *http.Request) {
	rep, err := h.Service.Activity(r.Context(), queryInt(r, "days", 0))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	loc := h.Service.Location()
	out := activityDTO{
		TimeZone:      loc.String(),
		From:          rep.From,
		To:            rep.To.In(loc),
		TrackingSince: rep.TrackingSince,
		Days:          make([]dayCountDTO, 0, len(rep.Days)),
		Hours:         make([]hourCountDTO, 0, len(rep.Hours)),
		Heatmap:       toSlots(rep.Heatmap),
		Peaks:         toSlots(rep.Peaks),
	}
	if t := rep.TrackingSince; t != nil {
		local := t.In(loc)
		out.TrackingSince = &local
	}
	for _, d := range rep.Days {
		out.Days = append(out.Days, dayCountDTO{Day: d.Day.Format(time.DateOnly), Users: d.Users})
	}
	for _, c := range rep.Hours {
		out.Hours = append(out.Hours, hourCountDTO{HourStart: c.Start.In(loc), Users: c.Users})
	}
	httperr.WriteJSON(w, http.StatusOK, out)
}
