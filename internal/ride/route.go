package ride

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Route limits.
const (
	MinStops        = 2
	MaxStops        = 8
	MaxSeats        = 8
	MinDuration     = 15 * time.Minute
	MaxDuration     = 48 * time.Hour
	MaxBookAhead    = 90 * 24 * time.Hour
	MaxMeetingPoint = 200
)

// StopType says whether passengers may board, leave, or both at a stop.
type StopType string

// Stop types.
const (
	StopPickup  StopType = "pickup"
	StopDropoff StopType = "dropoff"
	StopBoth    StopType = "both"
)

// AllowsPickup reports whether a passenger may board here.
func (t StopType) AllowsPickup() bool { return t == StopPickup || t == StopBoth }

// AllowsDropoff reports whether a passenger may leave here.
func (t StopType) AllowsDropoff() bool { return t == StopDropoff || t == StopBoth }

// Stop is one point of a route. Order 0 is the departure.
type Stop struct {
	Order        int
	LocationID   string
	MeetingPoint string
	PlannedAt    *time.Time
	Type         StopType
}

// NormalizeRoute trims meeting points, sets orders, fixes the endpoints' planned times,
// and validates the route against the trip schedule.
func NormalizeRoute(stops []Stop, departs, arrives time.Time) ([]Stop, error) {
	if len(stops) < MinStops || len(stops) > MaxStops {
		return nil, invalid("stops", "count")
	}
	seen := map[string]bool{}
	out := make([]Stop, len(stops))
	last := len(stops) - 1
	prev := departs
	for i, s := range stops {
		s.Order = i
		s.LocationID = strings.TrimSpace(s.LocationID)
		s.MeetingPoint = strings.TrimSpace(s.MeetingPoint)
		if !ValidID(s.LocationID) {
			return nil, invalid("stops", "location")
		}
		if seen[s.LocationID] {
			return nil, invalid("stops", "duplicate_location")
		}
		seen[s.LocationID] = true
		if utf8.RuneCountInString(s.MeetingPoint) > MaxMeetingPoint {
			return nil, invalid("stops", "meeting_point_length")
		}
		if s.Type == "" {
			s.Type = StopBoth
		}
		switch s.Type {
		case StopPickup, StopDropoff, StopBoth:
		default:
			return nil, invalid("stops", "type")
		}
		switch i {
		case 0:
			if !s.Type.AllowsPickup() {
				return nil, invalid("stops", "first_stop_type")
			}
			s.Type = StopPickup
			t := departs
			s.PlannedAt = &t
		case last:
			if !s.Type.AllowsDropoff() {
				return nil, invalid("stops", "last_stop_type")
			}
			s.Type = StopDropoff
			t := arrives
			s.PlannedAt = &t
		default:
			if s.PlannedAt != nil {
				if s.PlannedAt.Before(prev) || !s.PlannedAt.Before(arrives) {
					return nil, invalid("stops", "planned_at")
				}
				prev = *s.PlannedAt
			}
		}
		out[i] = s
	}
	return out, nil
}

// ValidateSchedule checks departure and arrival against now.
func ValidateSchedule(departs, arrives, now time.Time) error {
	if !departs.After(now) {
		return invalid("departs_at", "past")
	}
	if departs.After(now.Add(MaxBookAhead)) {
		return invalid("departs_at", "too_far")
	}
	d := arrives.Sub(departs)
	if d < MinDuration || d > MaxDuration {
		return invalid("arrives_at", "duration")
	}
	return nil
}

// ValidateSegment checks that pickup and drop-off are stops of the route in travel order
// and that the stop types allow boarding and leaving there.
func ValidateSegment(stops []Stop, pickup, dropoff int) error {
	if pickup < 0 || dropoff >= len(stops) || pickup >= dropoff {
		return ErrInvalidSegment
	}
	if !stops[pickup].Type.AllowsPickup() || !stops[dropoff].Type.AllowsDropoff() {
		return ErrInvalidSegment
	}
	return nil
}

// Occupant is the part of the route an approved booking occupies: segments
// From..To-1, where segment i runs from stop i to stop i+1.
type Occupant struct {
	From  int
	To    int
	Seats int
}

// SegmentLoad returns the approved seats on each of the stopCount-1 segments.
func SegmentLoad(occupants []Occupant, stopCount int) []int {
	if stopCount < 2 {
		return nil
	}
	load := make([]int, stopCount-1)
	for _, o := range occupants {
		for s := max(o.From, 0); s < o.To && s < len(load); s++ {
			load[s] += o.Seats
		}
	}
	return load
}

// FreeSeats is the number of seats free on every segment of [from, to).
func FreeSeats(capacity int, occupants []Occupant, stopCount, from, to int) int {
	load := SegmentLoad(occupants, stopCount)
	worst := 0
	for s := from; s < to && s < len(load); s++ {
		worst = max(worst, load[s])
	}
	return max(capacity-worst, 0)
}

// Fits reports whether seats more passengers fit on every segment of [from, to).
func Fits(capacity int, occupants []Occupant, stopCount, from, to, seats int) bool {
	return seats > 0 && FreeSeats(capacity, occupants, stopCount, from, to) >= seats
}

// IsFull reports whether no segment of the route has a free seat.
func IsFull(capacity int, occupants []Occupant, stopCount int) bool {
	for _, l := range SegmentLoad(occupants, stopCount) {
		if l < capacity {
			return false
		}
	}
	return true
}

// MaxLoad is the highest approved load on any segment.
func MaxLoad(occupants []Occupant, stopCount int) int {
	worst := 0
	for _, l := range SegmentLoad(occupants, stopCount) {
		worst = max(worst, l)
	}
	return worst
}

// StatusAfterLoad returns active or full for a bookable trip after a seat change.
func StatusAfterLoad(current TripStatus, capacity int, occupants []Occupant, stopCount int) TripStatus {
	if !current.Bookable() {
		return current
	}
	if IsFull(capacity, occupants, stopCount) {
		return TripFull
	}
	return TripActive
}
