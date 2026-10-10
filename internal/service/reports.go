package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shalarasgar/carpool-backend/internal/ratelimit"
	"github.com/shalarasgar/carpool-backend/internal/store"
)

// Report reasons accepted by POST /v1/reports.
const (
	ReportSpam            = "spam"
	ReportHarassment      = "harassment"
	ReportScam            = "scam"
	ReportUnsafeDriving   = "unsafe_driving"
	ReportNoShow          = "no_show"
	ReportOther           = "other"
	ReportStatusOpen      = "open"
	ReportStatusReview    = "reviewing"
	ReportStatusResolved  = "resolved"
	ReportStatusDismissed = "dismissed"
)

// ReportConfig is the per-user rate limit for filing reports.
type ReportConfig struct {
	Limit  int
	Window time.Duration
}

// ReportInput is POST /v1/reports.
type ReportInput struct {
	ReportedUserID string
	TripID         string
	Reason         string
	Details        *string
}

// Report is one filed report. Phone numbers are not part of it.
type Report struct {
	ID             string
	ReporterID     string
	ReportedUserID string
	TripID         *string
	Reason         string
	Details        *string
	Status         string
	CreatedAt      time.Time
}

// ReportPage is a page of reports for the admin list.
type ReportPage struct {
	Reports  []Report
	Page     int
	PageSize int
	HasMore  bool
}

// Reports files user reports.
type Reports struct {
	pool    *pgxpool.Pool
	q       *store.Queries
	limiter ratelimit.Limiter
	cfg     ReportConfig
	logger  *slog.Logger
}

// NewReports wires the report service. A non-positive limit uses 10 per 24h.
func NewReports(pool *pgxpool.Pool, limiter ratelimit.Limiter, cfg ReportConfig, logger *slog.Logger) *Reports {
	if logger == nil {
		logger = slog.Default()
	}
	if limiter == nil {
		limiter = ratelimit.NewMemory()
	}
	if cfg.Limit < 1 {
		cfg.Limit = 10
	}
	if cfg.Window <= 0 {
		cfg.Window = 24 * time.Hour
	}
	return &Reports{pool: pool, q: store.New(pool), limiter: limiter, cfg: cfg, logger: logger}
}

// Create files a report. The caller cannot report themselves. Invalid requests
// do not consume the per-user rate limit.
func (s *Reports) Create(ctx context.Context, reporterID string, in ReportInput) (Report, error) {
	reporter, err := parseUserID(reporterID)
	if err != nil {
		return Report{}, err
	}
	reported, err := parseFieldUUID(in.ReportedUserID, "reported_user_id")
	if err != nil {
		return Report{}, err
	}
	if sameID(reporter.String(), reported.String()) {
		return Report{}, ErrForbidden
	}
	if !knownReportReason(in.Reason) {
		return Report{}, validationError(map[string]string{"reason": "oneof"})
	}
	details, err := optionalText(in.Details, "details", 1000)
	if err != nil {
		return Report{}, err
	}
	var tripID pgtype.UUID
	if in.TripID != "" {
		tripID, err = parseFieldUUID(in.TripID, "trip_id")
		if err != nil {
			return Report{}, err
		}
	}

	reporterRow, err := s.q.GetUserByID(ctx, reporter)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && reporterRow.DeletedAt.Valid) {
		return Report{}, ErrUnauthorized
	}
	if err != nil {
		return Report{}, fmt.Errorf("load reporter: %w", err)
	}
	if reporterRow.IsBlocked {
		return Report{}, ErrUserBlocked
	}
	target, err := s.q.GetUserByID(ctx, reported)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && target.DeletedAt.Valid) {
		return Report{}, ErrNotFound
	}
	if err != nil {
		return Report{}, fmt.Errorf("load reported user: %w", err)
	}
	if tripID.Valid {
		if _, err := s.q.GetTripView(ctx, tripID); errors.Is(err, pgx.ErrNoRows) {
			return Report{}, validationError(map[string]string{"trip_id": "not_found"})
		} else if err != nil {
			return Report{}, fmt.Errorf("load trip: %w", err)
		}
	}

	decision, err := s.limiter.Allow(ctx, ratelimit.Rule{
		Key:    "report:" + reporter.String(),
		Limit:  s.cfg.Limit,
		Window: s.cfg.Window,
	})
	if err != nil {
		return Report{}, fmt.Errorf("rate limit: %w", err)
	}
	if !decision.Allowed {
		return Report{}, &RateLimitError{RetryAfter: decision.RetryAfter}
	}

	row, err := s.q.CreateReport(ctx, store.CreateReportParams{
		ReporterID:     reporter,
		ReportedUserID: reported,
		TripID:         tripID,
		Reason:         in.Reason,
		Details:        details,
	})
	if err != nil {
		return Report{}, fmt.Errorf("create report: %w", err)
	}
	s.logger.Info("report filed", "user_id", reporterID, "reported_user_id", reported.String(), "report_id", row.ID.String())
	return reportFrom(row), nil
}

func knownReportReason(reason string) bool {
	switch reason {
	case ReportSpam, ReportHarassment, ReportScam, ReportUnsafeDriving, ReportNoShow, ReportOther:
		return true
	default:
		return false
	}
}

func knownReportStatus(status string) bool {
	switch status {
	case ReportStatusOpen, ReportStatusReview, ReportStatusResolved, ReportStatusDismissed:
		return true
	default:
		return false
	}
}

func reportFrom(row store.Report) Report {
	out := Report{
		ID:             row.ID.String(),
		ReporterID:     row.ReporterID.String(),
		ReportedUserID: row.ReportedUserID.String(),
		Reason:         row.Reason,
		Details:        textPtr(row.Details),
		Status:         row.Status,
		CreatedAt:      row.CreatedAt.Time,
	}
	if row.TripID.Valid {
		id := row.TripID.String()
		out.TripID = &id
	}
	return out
}

func optionalText(raw *string, field string, maxRunes int) (pgtype.Text, error) {
	if raw == nil {
		return pgtype.Text{}, nil
	}
	s := strings.TrimSpace(*raw)
	if s == "" {
		return pgtype.Text{}, nil
	}
	if utf8.RuneCountInString(s) > maxRunes {
		return pgtype.Text{}, validationError(map[string]string{field: "max"})
	}
	return pgtype.Text{String: s, Valid: true}, nil
}
