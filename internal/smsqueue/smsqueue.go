// Package smsqueue is the SMS job state machine shared by the service and the database.
// The phone is the only component that talks to the cellular radio. This package decides
// when a job may be claimed again and when a result is final.
package smsqueue

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// HealthyWindow is how recently a gateway must have heartbeated to count as available.
	// The readiness check uses the same window.
	HealthyWindow = 30 * time.Second

	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusSent       = "sent"
	StatusFailed     = "failed"

	DeliveryConfirmed = "confirmed"
	DeliveryFailed    = "failed"
	DeliveryUnknown   = "unknown"

	ReportSent           = "sent"
	ReportFailed         = "failed"
	ReportUnknown        = "unknown"
	ReportDelivered      = "delivered"
	ReportDeliveryFailed = "delivery_failed"
)

var (
	// ErrNotFound means the job or gateway does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConflict means the job is not in a state that accepts this transition.
	ErrConflict = errors.New("conflict")
	// ErrAlreadySent means the telephony system already accepted the message.
	ErrAlreadySent = errors.New("already sent")
	// ErrUnauthorized means the gateway credential is missing, unknown, or revoked.
	ErrUnauthorized = errors.New("unauthorized")
)

// RateLimited is returned when an OTP or gateway request exceeds its quota.
type RateLimited struct {
	RetryAfter time.Duration
}

func (e *RateLimited) Error() string { return "rate limited" }

// Job is one outbound SMS authorized by the backend.
type Job struct {
	ID                  string
	Phone               string
	Message             string
	Status              string
	Attempts            int
	GatewayID           string
	LastError           string
	LockedUntil         time.Time
	SendStartedAt       time.Time
	TelephonyAcceptedAt time.Time
	DeliveryStatus      string
	SentAt              time.Time
	CreatedAt           time.Time
}

// Gateway is a phone allowed to claim jobs.
type Gateway struct {
	ID            string
	Name          string
	Prefixes      []string
	Active        bool
	LastHeartbeat time.Time
	CreatedAt     time.Time
}

// Report is a result posted by the gateway that claimed the job.
type Report struct {
	ID           string
	Status       string
	Error        string
	NotSubmitted bool
}

var (
	longDigits = regexp.MustCompile(`\d{5,}`)
	e164       = regexp.MustCompile(`\+[0-9]{8,15}`)
)

// RedactError removes codes and phone numbers from a client-supplied error string.
func RedactError(s string) string {
	s = strings.TrimSpace(s)
	s = e164.ReplaceAllString(s, "+***")
	s = longDigits.ReplaceAllString(s, "*****")
	if utf8.RuneCountInString(s) > 400 {
		s = string([]rune(s)[:400])
	}
	return s
}

// MaskPhone keeps the country prefix and the last two digits.
func MaskPhone(phone string) string {
	if len(phone) < 6 {
		return "***"
	}
	return phone[:4] + strings.Repeat("*", len(phone)-6) + phone[len(phone)-2:]
}

// Eligible reports whether claimer may take a pending job.
// A job pinned to a gateway that heartbeated inside HealthyWindow stays with that gateway.
func Eligible(job Job, claimer Gateway, healthy map[string]struct{}) bool {
	if job.Status != StatusPending || !claimer.Active {
		return false
	}
	if job.GatewayID != "" && job.GatewayID != claimer.ID {
		if _, ok := healthy[job.GatewayID]; ok {
			return false
		}
	}
	if len(claimer.Prefixes) == 0 {
		return true
	}
	national := strings.TrimPrefix(job.Phone, "+993")
	for _, prefix := range claimer.Prefixes {
		if prefix != "" && strings.HasPrefix(national, prefix) {
			return true
		}
	}
	return false
}

// ChooseGateway picks a healthy gateway for a new message.
// A prefix match wins. Otherwise the first healthy gateway with no prefixes is used.
// ok is false when every gateway is stale; the caller still enqueues the message.
func ChooseGateway(phone string, gateways []Gateway, now time.Time) (string, bool) {
	national := strings.TrimPrefix(phone, "+993")
	fallback := ""
	for _, g := range gateways {
		if !healthy(g, now) {
			continue
		}
		if len(g.Prefixes) == 0 {
			if fallback == "" {
				fallback = g.ID
			}
			continue
		}
		for _, prefix := range g.Prefixes {
			if prefix != "" && strings.HasPrefix(national, prefix) {
				return g.ID, true
			}
		}
	}
	if fallback == "" {
		return "", false
	}
	return fallback, true
}

func healthy(g Gateway, now time.Time) bool {
	if !g.Active || g.LastHeartbeat.IsZero() {
		return false
	}
	return now.Sub(g.LastHeartbeat) <= HealthyWindow
}

// MarkSending records that this gateway is about to hand the message to the radio.
// After this succeeds, a crash must not return the job to the general pending pool.
func MarkSending(job Job, lease time.Duration, now time.Time) (Job, error) {
	switch job.Status {
	case StatusSent:
		return job, ErrAlreadySent
	case StatusProcessing:
		if job.SendStartedAt.IsZero() {
			job.SendStartedAt = now
		}
		job.LockedUntil = now.Add(lease)
		return job, nil
	default:
		return job, ErrConflict
	}
}

// ApplyReport moves a job according to a gateway result.
// sent means the Android telephony stack accepted the message, not that the carrier delivered it.
// A failed report is retryable only when the radio never accepted the message.
func ApplyReport(job Job, in Report, maxAttempts int, now time.Time) (Job, error) {
	in.Error = RedactError(in.Error)
	switch in.Status {
	case ReportDelivered:
		if job.Status != StatusSent {
			return job, ErrConflict
		}
		job.DeliveryStatus = DeliveryConfirmed
		return job, nil
	case ReportDeliveryFailed:
		if job.Status != StatusSent || job.DeliveryStatus == DeliveryConfirmed {
			return job, ErrConflict
		}
		job.DeliveryStatus = DeliveryFailed
		if in.Error != "" {
			job.LastError = in.Error
		}
		return job, nil
	case ReportSent:
		return applySent(job, now)
	case ReportUnknown:
		return applyUnknown(job, in.Error, now)
	case ReportFailed:
		return applyFailed(job, in, maxAttempts)
	default:
		return job, ErrConflict
	}
}

func applySent(job Job, now time.Time) (Job, error) {
	if job.Status == StatusSent {
		return job, nil
	}
	late := job.Status == StatusFailed &&
		job.DeliveryStatus == DeliveryUnknown &&
		job.SentAt.IsZero() &&
		!job.SendStartedAt.IsZero()
	if job.Status != StatusProcessing && !late {
		return job, ErrConflict
	}
	if job.SendStartedAt.IsZero() {
		job.SendStartedAt = now
	}
	job.Status = StatusSent
	job.TelephonyAcceptedAt = now
	job.SentAt = now
	job.LockedUntil = time.Time{}
	job.LastError = ""
	job.DeliveryStatus = ""
	return job, nil
}

func applyUnknown(job Job, reason string, now time.Time) (Job, error) {
	if job.Status == StatusSent {
		return job, nil
	}
	if job.Status == StatusFailed && job.DeliveryStatus == DeliveryUnknown {
		return job, nil
	}
	if job.Status != StatusProcessing {
		return job, ErrConflict
	}
	job.Status = StatusFailed
	job.DeliveryStatus = DeliveryUnknown
	job.LockedUntil = time.Time{}
	job.Attempts++
	if reason == "" {
		reason = "transmission outcome was not confirmed"
	}
	job.LastError = "unknown: " + reason
	_ = now
	return job, nil
}

func applyFailed(job Job, in Report, maxAttempts int) (Job, error) {
	if job.Status == StatusSent {
		return job, ErrConflict
	}
	if job.Status == StatusFailed {
		return job, nil
	}
	if job.Status != StatusProcessing {
		return job, ErrConflict
	}
	job.Attempts++
	job.LockedUntil = time.Time{}
	reason := in.Error
	if reason == "" {
		reason = "send failed"
	}
	// NotSubmitted is an explicit statement that SmsManager was never called.
	// A send that already started is terminal: another automatic send could duplicate the SMS.
	retryable := (job.SendStartedAt.IsZero() || in.NotSubmitted) && job.Attempts < maxAttempts
	if !job.SendStartedAt.IsZero() && !in.NotSubmitted {
		retryable = false
	}
	if retryable {
		job.Status = StatusPending
		job.GatewayID = ""
		job.SendStartedAt = time.Time{}
		job.TelephonyAcceptedAt = time.Time{}
		job.DeliveryStatus = ""
		job.LastError = reason
		return job, nil
	}
	job.Status = StatusFailed
	job.LastError = reason
	return job, nil
}

// Expire applies lease and age rules.
// Messages that were never handed to the radio can be claimed again.
// Messages whose send already started become unknown and are not queued again.
// Messages older than maxAge are failed because the OTP they carry has expired.
func Expire(job Job, maxAge time.Duration, now time.Time) (Job, bool) {
	started := !job.SendStartedAt.IsZero()
	if !started && !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > maxAge &&
		(job.Status == StatusPending || job.Status == StatusProcessing) {
		job.Status = StatusFailed
		job.LastError = "expired"
		job.LockedUntil = time.Time{}
		job.DeliveryStatus = ""
		return job, true
	}
	if job.Status != StatusProcessing || job.LockedUntil.IsZero() || !now.After(job.LockedUntil) {
		return job, false
	}
	if !started {
		job.Status = StatusPending
		job.GatewayID = ""
		job.LockedUntil = time.Time{}
		return job, true
	}
	if job.TelephonyAcceptedAt.IsZero() {
		job.Status = StatusFailed
		job.DeliveryStatus = DeliveryUnknown
		job.LastError = "unknown: lease expired after send started"
		job.LockedUntil = time.Time{}
		job.Attempts++
		return job, true
	}
	return job, false
}

// SMSText is the single SMS segment sent to the phone. The gateway must send it unchanged.
// The Turkmen text is not GSM-7, so it is sent as UCS-2 and must stay within 70 characters.
func SMSText(locale, appName, code string) string {
	name := SMSName(appName)
	switch locale {
	case "ru":
		return fmt.Sprintf("%s: kod %s. Nikomu ne soobshayte.", name, code)
	case "en":
		return fmt.Sprintf("%s: code %s. Do not share it.", name, code)
	default:
		return fmt.Sprintf("%s: kod %s. Kody hiçkim bilen paýlaşmaň!", name, code)
	}
}

// SMSName keeps letters (Turkmen included), digits and single spaces, capped at 16 runes
// so the longest template still fits one 70-character UCS-2 segment.
func SMSName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			b.WriteRune(r)
		}
	}
	runes := []rune(strings.Join(strings.Fields(b.String()), " "))
	if len(runes) > 16 {
		runes = runes[:16]
	}
	s := strings.TrimSpace(string(runes))
	if s == "" {
		return "carpool"
	}
	return s
}
