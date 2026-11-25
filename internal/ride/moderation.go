package ride

import (
	"strings"
	"time"
	"unicode/utf8"
)

// MaxReportDetails is the longest free text a report may carry.
const MaxReportDetails = 1000

// ReportReason says why a user was reported.
type ReportReason string

// Report reasons. The list is closed, so the moderation queue can be filtered and the
// client can translate each value.
const (
	ReasonSpam          ReportReason = "spam"
	ReasonFraud         ReportReason = "fraud"
	ReasonAbuse         ReportReason = "abuse"
	ReasonNoShow        ReportReason = "no_show"
	ReasonUnsafe        ReportReason = "unsafe"
	ReasonFakeProfile   ReportReason = "fake_profile"
	ReasonInappropriate ReportReason = "inappropriate_content"
	ReasonOther         ReportReason = "other"
)

// Valid reports whether r is a known reason.
func (r ReportReason) Valid() bool {
	switch r {
	case ReasonSpam, ReasonFraud, ReasonAbuse, ReasonNoShow, ReasonUnsafe,
		ReasonFakeProfile, ReasonInappropriate, ReasonOther:
		return true
	}
	return false
}

// ReportStatus is the moderation state of a report.
type ReportStatus string

// Report statuses.
const (
	ReportOpen     ReportStatus = "open"
	ReportResolved ReportStatus = "resolved"
)

// Valid reports whether s is a known status.
func (s ReportStatus) Valid() bool { return s == ReportOpen || s == ReportResolved }

// ReportInput is a new report about another user, optionally tied to one trip.
type ReportInput struct {
	ReporterID     string
	ReportedUserID string
	TripID         string
	Reason         ReportReason
	Details        string
}

// Normalize trims the report and validates it. "other" needs details, because the reason
// alone tells a moderator nothing.
func (in *ReportInput) Normalize() error {
	if !ValidID(in.ReportedUserID) {
		return invalid("reported_user_id", "required")
	}
	if in.ReportedUserID == in.ReporterID {
		return invalid("reported_user_id", "self")
	}
	if in.TripID != "" && !ValidID(in.TripID) {
		return invalid("trip_id", "not_found")
	}
	if !in.Reason.Valid() {
		return invalid("reason", "unknown")
	}
	in.Details = strings.TrimSpace(in.Details)
	if utf8.RuneCountInString(in.Details) > MaxReportDetails {
		return invalid("details", "length")
	}
	if in.Reason == ReasonOther && in.Details == "" {
		return invalid("details", "required")
	}
	return nil
}

// Report is a report as a moderator sees it. It carries names, never phone numbers.
type Report struct {
	ID              string
	ReporterID      string
	ReporterName    string
	ReportedUserID  string
	ReportedName    string
	ReportedBlocked bool
	ReportedDeleted bool
	TripID          string
	Reason          ReportReason
	Details         string
	Status          ReportStatus
	CreatedAt       time.Time
	ResolvedAt      *time.Time
	// History of the reported user, so the queue shows repeat offenders at a glance.
	ReportedReports     int
	ReportedOpenReports int
	ReportedReporters   int
}
