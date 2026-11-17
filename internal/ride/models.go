package ride

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Profile is a user account. One account both drives and travels as a passenger.
type Profile struct {
	ID               string
	Phone            string
	FirstName        string
	LastName         string
	Bio              string
	PhotoURL         string
	Locale           string
	RatingAvg        float64
	RatingCount      int
	TripsAsDriver    int
	TripsAsPassenger int
	CreatedAt        time.Time
}

// Complete reports whether the mandatory profile fields are set.
func (p Profile) Complete() bool { return p.FirstName != "" && p.LastName != "" }

// ProfilePatch is a partial profile update. Nil fields are left unchanged.
type ProfilePatch struct {
	FirstName *string
	LastName  *string
	Bio       *string
	Locale    *string
}

// Normalize trims the patch and validates it.
func (p *ProfilePatch) Normalize() error {
	if p.FirstName != nil {
		v := strings.TrimSpace(*p.FirstName)
		if v == "" || utf8.RuneCountInString(v) > 50 {
			return invalid("first_name", "length")
		}
		p.FirstName = &v
	}
	if p.LastName != nil {
		v := strings.TrimSpace(*p.LastName)
		if v == "" || utf8.RuneCountInString(v) > 50 {
			return invalid("last_name", "length")
		}
		p.LastName = &v
	}
	if p.Bio != nil {
		v := strings.TrimSpace(*p.Bio)
		if utf8.RuneCountInString(v) > 500 {
			return invalid("bio", "length")
		}
		p.Bio = &v
	}
	if p.Locale != nil && *p.Locale != "tk" && *p.Locale != "ru" && *p.Locale != "en" {
		return invalid("locale", "unsupported")
	}
	return nil
}

// UserSummary is the public face of a user next to a trip or booking. It never has a phone.
type UserSummary struct {
	ID            string
	FirstName     string
	LastName      string
	PhotoURL      string
	RatingAvg     float64
	RatingCount   int
	TripsAsDriver int
	// Deleted marks an erased account. Its name and photo are empty; clients show a
	// placeholder such as "Deleted user".
	Deleted bool
}

// Vehicle is a car owned by a user. PassengerSeats excludes the driver's seat.
type Vehicle struct {
	ID             string
	UserID         string
	Brand          string
	Model          string
	Color          string
	Plate          string
	Year           *int
	Type           string
	PassengerSeats *int
	PhotoURL       string
	CreatedAt      time.Time
}

var vehicleTypes = map[string]bool{
	"": true, "sedan": true, "hatchback": true, "wagon": true, "suv": true, "minivan": true, "minibus": true, "other": true,
}

// Normalize trims the vehicle fields and validates them.
func (v *Vehicle) Normalize() error {
	v.Brand = strings.TrimSpace(v.Brand)
	v.Model = strings.TrimSpace(v.Model)
	v.Color = strings.TrimSpace(v.Color)
	v.Plate = strings.ToUpper(strings.Join(strings.Fields(v.Plate), " "))
	checks := []struct {
		field, value string
		max          int
	}{{"brand", v.Brand, 40}, {"model", v.Model, 40}, {"color", v.Color, 30}, {"plate", v.Plate, 20}}
	for _, c := range checks {
		if c.value == "" || utf8.RuneCountInString(c.value) > c.max {
			return invalid(c.field, "length")
		}
	}
	if v.Year != nil && (*v.Year < 1950 || *v.Year > 2100) {
		return invalid("year", "range")
	}
	if !vehicleTypes[v.Type] {
		return invalid("vehicle_type", "unsupported")
	}
	if v.PassengerSeats != nil && (*v.PassengerSeats < 1 || *v.PassengerSeats > MaxSeats) {
		return invalid("passenger_seats", "range")
	}
	return nil
}

// Amenities are the structured comfort options of a trip. Empty means "unspecified".
type Amenities struct {
	AirConditioning string
	AudioSystem     string
	MusicPolicy     string
	SmokingPolicy   string
	PetsPolicy      string
	LuggageSize     string
	ChildSeat       string
	Conversation    string
}

var amenityValues = map[string]map[string]bool{
	"air_conditioning": {"yes": true, "no": true},
	"audio_system":     {"yes": true, "no": true},
	"music_policy":     {"allowed": true, "none": true, "driver_decides": true},
	"smoking_policy":   {"allowed": true, "prohibited": true},
	"pets_policy":      {"allowed": true, "not_allowed": true},
	"luggage_size":     {"none": true, "small": true, "medium": true, "large": true},
	"child_seat":       {"yes": true, "no": true},
	"conversation":     {"friendly": true, "quiet": true, "no_preference": true},
}

// Unspecified is the stored value of an option the driver left empty.
const Unspecified = "unspecified"

func (a *Amenities) fields() []struct {
	name string
	ptr  *string
} {
	return []struct {
		name string
		ptr  *string
	}{
		{"air_conditioning", &a.AirConditioning}, {"audio_system", &a.AudioSystem},
		{"music_policy", &a.MusicPolicy}, {"smoking_policy", &a.SmokingPolicy},
		{"pets_policy", &a.PetsPolicy}, {"luggage_size", &a.LuggageSize},
		{"child_seat", &a.ChildSeat}, {"conversation", &a.Conversation},
	}
}

// Normalize maps empty options to "unspecified" and rejects unknown values.
func (a *Amenities) Normalize() error {
	for _, f := range a.fields() {
		if *f.ptr == "" {
			*f.ptr = Unspecified
			continue
		}
		if *f.ptr != Unspecified && !amenityValues[f.name][*f.ptr] {
			return invalid(f.name, "unsupported")
		}
	}
	return nil
}

// LuggageRank orders luggage sizes for "at least this size" filters.
func LuggageRank(size string) int {
	switch size {
	case "small":
		return 1
	case "medium":
		return 2
	case "large":
		return 3
	default:
		return 0
	}
}

// ValidLuggage reports whether a booking luggage value is known.
func ValidLuggage(v string) bool {
	return v == "" || v == Unspecified || amenityValues["luggage_size"][v]
}

// Trip is a published ride with its route. Segments holds the free seats of each segment.
type Trip struct {
	ID              string
	Status          TripStatus
	Driver          UserSummary
	Vehicle         *Vehicle
	Stops           []StopView
	DepartsAt       time.Time
	ArrivesAt       time.Time
	SeatsTotal      int
	Price           *float64
	PriceNegotiable bool
	Currency        string
	Description     string
	Conditions      string
	Amenities       Amenities
	FreeBySegment   []int
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// Match is set by search: the stops that matched the requested route.
	Match *SegmentMatch
	// Viewer is set when the trip is loaded for a signed-in user.
	Viewer *TripViewer
	// Counts for the driver's own list.
	PendingRequests int
	ApprovedSeats   int
}

// StopView is a stop with its localized location.
type StopView struct {
	Stop
	Location Location
}

// SegmentMatch is the matched pickup and drop-off and the seats free between them.
type SegmentMatch struct {
	PickupOrder  int
	DropoffOrder int
	FreeSeats    int
}

// TripViewer is the viewer's relation to a trip.
type TripViewer struct {
	IsDriver      bool
	BookingID     string
	BookingStatus BookingStatus
}

// CanSeePlate reports whether the plate may be shown to this viewer.
func (v *TripViewer) CanSeePlate() bool {
	return v != nil && (v.IsDriver || v.BookingStatus.ContactVisible())
}

// TripInput is a new trip from the driver.
type TripInput struct {
	DriverID        string
	VehicleID       string
	Stops           []Stop
	DepartsAt       time.Time
	ArrivesAt       time.Time
	SeatsTotal      int
	Price           *float64
	PriceNegotiable bool
	Description     string
	Conditions      string
	Amenities       Amenities
}

// Normalize validates the trip against now and returns the normalized route.
func (in *TripInput) Normalize(now time.Time) error {
	if strings.TrimSpace(in.VehicleID) == "" {
		return invalid("vehicle_id", "required")
	}
	if err := ValidateSchedule(in.DepartsAt, in.ArrivesAt, now); err != nil {
		return err
	}
	stops, err := NormalizeRoute(in.Stops, in.DepartsAt, in.ArrivesAt)
	if err != nil {
		return err
	}
	in.Stops = stops
	if in.SeatsTotal < 1 || in.SeatsTotal > MaxSeats {
		return invalid("seats_total", "range")
	}
	if err := validatePrice(in.Price); err != nil {
		return err
	}
	in.Description = strings.TrimSpace(in.Description)
	in.Conditions = strings.TrimSpace(in.Conditions)
	if utf8.RuneCountInString(in.Description) > 1000 {
		return invalid("description", "length")
	}
	if utf8.RuneCountInString(in.Conditions) > 500 {
		return invalid("conditions", "length")
	}
	return in.Amenities.Normalize()
}

func validatePrice(p *float64) error {
	if p != nil && (*p < 0 || *p > 100000) {
		return invalid("price", "range")
	}
	return nil
}

// TripPatch is a partial trip update by its driver.
type TripPatch struct {
	VehicleID       *string
	DepartsAt       *time.Time
	ArrivesAt       *time.Time
	SeatsTotal      *int
	Price           *float64
	ClearPrice      bool
	PriceNegotiable *bool
	Description     *string
	Conditions      *string
	Amenities       *Amenities
	// MeetingPoints maps stop order to a new meeting point.
	MeetingPoints map[int]string
}

// Normalize validates the fields that do not need the stored trip.
func (p *TripPatch) Normalize() error {
	if p.SeatsTotal != nil && (*p.SeatsTotal < 1 || *p.SeatsTotal > MaxSeats) {
		return invalid("seats_total", "range")
	}
	if err := validatePrice(p.Price); err != nil {
		return err
	}
	if p.Description != nil {
		v := strings.TrimSpace(*p.Description)
		if utf8.RuneCountInString(v) > 1000 {
			return invalid("description", "length")
		}
		p.Description = &v
	}
	if p.Conditions != nil {
		v := strings.TrimSpace(*p.Conditions)
		if utf8.RuneCountInString(v) > 500 {
			return invalid("conditions", "length")
		}
		p.Conditions = &v
	}
	if p.Amenities != nil {
		if err := p.Amenities.Normalize(); err != nil {
			return err
		}
	}
	for order, mp := range p.MeetingPoints {
		mp = strings.TrimSpace(mp)
		if utf8.RuneCountInString(mp) > MaxMeetingPoint {
			return invalid("meeting_points", "length")
		}
		p.MeetingPoints[order] = mp
	}
	if (p.DepartsAt == nil) != (p.ArrivesAt == nil) {
		return invalid("arrives_at", "both_required")
	}
	return nil
}

// ScheduleChange reports whether the patch moves the trip in time.
func (p *TripPatch) ScheduleChange() bool { return p.DepartsAt != nil }

// Location is a welayat, city, etrap, or settlement with names in the three app languages.
type Location struct {
	ID        string
	ParentID  string
	Type      string
	NameTK    string
	NameRU    string
	NameEN    string
	SortOrder int
}

// SearchQuery is a passenger trip search. Empty from/to match any stop.
type SearchQuery struct {
	ViewerID    string
	FromID      string
	ToID        string
	Date        *time.Time // local calendar date in the service time zone
	Seats       int
	TimeFrom    string // HH:MM, local
	TimeTo      string // HH:MM, local
	PriceMin    *float64
	PriceMax    *float64
	AirCond     bool
	PetsAllowed bool
	NoSmoking   bool
	SmokingOK   bool
	ChildSeat   bool
	MinLuggage  string
	MinRating   *float64
	Sort        string
	Page        int
	Limit       int
}

// Normalize validates and fills defaults.
func (q *SearchQuery) Normalize() error {
	if q.Seats == 0 {
		q.Seats = 1
	}
	if q.Seats < 1 || q.Seats > MaxSeats {
		return invalid("seats", "range")
	}
	if q.FromID != "" && q.FromID == q.ToID {
		return invalid("to", "same_as_from")
	}
	for _, t := range []struct{ f, v string }{{"time_from", q.TimeFrom}, {"time_to", q.TimeTo}} {
		if t.v == "" {
			continue
		}
		if _, err := time.Parse("15:04", t.v); err != nil {
			return invalid(t.f, "format")
		}
	}
	switch q.Sort {
	case "":
		q.Sort = "departure"
	case "departure", "price_asc", "price_desc", "rating":
	default:
		return invalid("sort", "unsupported")
	}
	if q.MinLuggage != "" && LuggageRank(q.MinLuggage) == 0 {
		return invalid("luggage", "unsupported")
	}
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Limit < 1 || q.Limit > 50 {
		q.Limit = 20
	}
	return nil
}

// Booking is a passenger request for seats on part of a trip.
type Booking struct {
	ID           string
	TripID       string
	Status       BookingStatus
	Seats        int
	PickupOrder  int
	DropoffOrder int
	Luggage      string
	Message      string
	CreatedAt    time.Time
	DecidedAt    *time.Time
	CancelledAt  *time.Time
	CompletedAt  *time.Time
	Passenger    UserSummary
	Trip         *Trip
	// ContactPhone is the other participant's phone. It is set only when the viewer
	// participates in the booking and the status allows contact.
	ContactPhone string
	// Reviewed is true when the viewer already reviewed this booking.
	Reviewed bool
}

// BookingInput is a new seat request.
type BookingInput struct {
	PassengerID  string
	TripID       string
	PickupOrder  int
	DropoffOrder int
	Seats        int
	Luggage      string
	Message      string
}

// Normalize validates the request fields that do not need the trip.
func (in *BookingInput) Normalize() error {
	if in.Seats < 1 || in.Seats > MaxSeats {
		return invalid("seats", "range")
	}
	if !ValidLuggage(in.Luggage) {
		return invalid("luggage", "unsupported")
	}
	if in.Luggage == "" {
		in.Luggage = Unspecified
	}
	in.Message = strings.TrimSpace(in.Message)
	if utf8.RuneCountInString(in.Message) > 500 {
		return invalid("message", "length")
	}
	return nil
}

// Review is a rating left after a completed booking.
type Review struct {
	ID        string
	BookingID string
	TripID    string
	Author    UserSummary
	ToUserID  string
	Rating    int
	Comment   string
	// AuthorRole is "passenger" when a passenger rated the driver, "driver" otherwise.
	AuthorRole string
	CreatedAt  time.Time
}

// ReviewInput is a new review.
type ReviewInput struct {
	BookingID string
	AuthorID  string
	Rating    int
	Comment   string
}

// Normalize validates the review fields.
func (in *ReviewInput) Normalize() error {
	if in.Rating < 1 || in.Rating > 5 {
		return invalid("rating", "range")
	}
	in.Comment = strings.TrimSpace(in.Comment)
	if utf8.RuneCountInString(in.Comment) > 1000 {
		return invalid("comment", "length")
	}
	return nil
}

// NotificationType names an inbox event. The client renders the localized text.
type NotificationType string

// Notification types.
const (
	NoteBookingRequested  NotificationType = "booking_requested"
	NoteBookingApproved   NotificationType = "booking_approved"
	NoteBookingRejected   NotificationType = "booking_rejected"
	NoteBookingCancelledP NotificationType = "booking_cancelled_by_passenger"
	NoteBookingCancelledD NotificationType = "booking_cancelled_by_driver"
	NoteBookingExpired    NotificationType = "booking_expired"
	NoteTripCancelled     NotificationType = "trip_cancelled"
	NoteTripUpdated       NotificationType = "trip_updated"
	NoteTripReminder      NotificationType = "trip_reminder"
	NoteTripCompleted     NotificationType = "trip_completed"
	NoteReviewReceived    NotificationType = "review_received"
)

// Notification is an inbox row. It carries references, never phone numbers or codes.
type Notification struct {
	ID        string
	UserID    string
	Type      NotificationType
	TripID    string
	BookingID string
	Actor     *UserSummary
	ReadAt    *time.Time
	CreatedAt time.Time
	// Route is a short "first stop -> last stop" summary for rendering.
	From      *Location
	To        *Location
	DepartsAt *time.Time
}

// Event is one notification to deliver. DedupeKey makes delivery idempotent per user.
type Event struct {
	UserID    string
	Type      NotificationType
	TripID    string
	BookingID string
	ActorID   string
	DedupeKey string
}

// PushJob is an inbox row claimed for push delivery, with what its text needs in the
// recipient's language and the recipient's device tokens.
type PushJob struct {
	NotificationID string
	Type           NotificationType
	TripID         string
	BookingID      string
	CreatedAt      time.Time
	Locale         string
	From           *Location
	To             *Location
	Actor          *UserSummary
	Tokens         []string
}
