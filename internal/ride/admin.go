package ride

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ReportFilter narrows the moderation queue. An empty field matches every report.
type ReportFilter struct {
	Status         ReportStatus
	Reason         ReportReason
	ReportedUserID string
	ReporterID     string
}

// Validate rejects unknown values. A malformed user id is a validation error rather than
// ErrNotFound: a filter names no resource, so nothing was not found.
func (f ReportFilter) Validate() error {
	if f.Status != "" && !f.Status.Valid() {
		return invalid("status", "unknown")
	}
	if f.Reason != "" && !f.Reason.Valid() {
		return invalid("reason", "unknown")
	}
	if f.ReportedUserID != "" && !ValidID(f.ReportedUserID) {
		return invalid("reported_user_id", "format")
	}
	if f.ReporterID != "" && !ValidID(f.ReporterID) {
		return invalid("reporter_id", "format")
	}
	return nil
}

// UserState filters the admin account list.
type UserState string

// Account states. The empty state lists every account that is not deleted, because
// erased rows have no name or phone and only clutter a search.
const (
	UserStateLive    UserState = ""
	UserStateActive  UserState = "active"
	UserStateBlocked UserState = "blocked"
	UserStateDeleted UserState = "deleted"
)

// Valid reports whether s is a known state.
func (s UserState) Valid() bool {
	switch s {
	case UserStateLive, UserStateActive, UserStateBlocked, UserStateDeleted:
		return true
	}
	return false
}

// MaxUserQuery is the longest admin search text.
const MaxUserQuery = 100

// minPhoneDigits is the shortest digit run that searches phone numbers. Fewer digits
// would match nearly every account.
const minPhoneDigits = 3

// UserQuery is an admin search over accounts by name or phone.
type UserQuery struct {
	Text  string
	State UserState
	// Digits is the phone fragment taken from Text, set by Normalize. It is empty when
	// Text has fewer than three digits, and then phones are not searched.
	Digits string
}

// Normalize trims the query, validates it, and extracts the phone digits, so "+993 65"
// and "99365" find the same numbers.
func (q *UserQuery) Normalize() error {
	q.Text = strings.TrimSpace(q.Text)
	if utf8.RuneCountInString(q.Text) > MaxUserQuery {
		return invalid("q", "too_long")
	}
	if !q.State.Valid() {
		return invalid("status", "unknown")
	}
	var b strings.Builder
	for _, r := range q.Text {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	q.Digits = ""
	if b.Len() >= minPhoneDigits {
		q.Digits = b.String()
	}
	return nil
}

// HasLetters reports whether the query can match a name. A query of digits and
// punctuation only searches phones.
func (q UserQuery) HasLetters() bool {
	return strings.IndexFunc(q.Text, unicode.IsLetter) >= 0
}

// AdminUser is an account as a moderator sees it, phone included.
type AdminUser struct {
	ID          string
	Phone       string
	FirstName   string
	LastName    string
	PhotoURL    string
	RatingAvg   float64
	RatingCount int
	Blocked     bool
	Deleted     bool
	CreatedAt   time.Time
	// OpenReports counts unresolved reports about the account.
	OpenReports int
}

// UserActivity counts what an account did, so a moderator can weigh a report against
// the person's record.
type UserActivity struct {
	// As a driver.
	TripsUpcoming  int
	TripsCompleted int
	TripsCancelled int
	// DriverCancellations counts bookings on the user's trips that the driver cancelled,
	// including those ended by cancelling the whole trip.
	DriverCancellations int

	// As a passenger.
	BookingsActive    int
	BookingsCompleted int
	// PassengerCancellations counts the user's own bookings they cancelled.
	PassengerCancellations int

	// Moderation.
	ReportsAgainst     int
	ReportsAgainstOpen int
	// Reporters counts distinct people who reported the user. Five reports from five
	// people weigh more than five from one.
	Reporters    int
	ReportsFiled int
	// BlockedBy counts users who blocked this account.
	BlockedBy int
}

// AdminUserDetail is the moderator's account card.
type AdminUserDetail struct {
	AdminUser
	Bio       string
	Locale    string
	DeletedAt *time.Time
	Activity  UserActivity
	Vehicles  []Vehicle
}

// Overview is the admin dashboard: queue sizes, the current local day's activity, and
// SMS gateway health.
type Overview struct {
	// Since is the start of the local day that the "new" and "failed" counts cover.
	Since time.Time

	ReportsOpen int

	UsersTotal   int
	UsersNew     int
	UsersBlocked int

	TripsUpcoming int
	TripsNew      int

	BookingsPending int
	BookingsNew     int

	SMSPending     int
	SMSFailed      int
	GatewaysActive int
	// LastHeartbeat is the newest heartbeat of an active gateway; nil if none has called in.
	LastHeartbeat *time.Time
}

// DayStart returns midnight of now's calendar day in loc.
func DayStart(now time.Time, loc *time.Location) time.Time {
	t := now.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}
