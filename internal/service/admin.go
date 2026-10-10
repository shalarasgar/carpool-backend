package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shalarasgar/carpool-backend/internal/notify"
	"github.com/shalarasgar/carpool-backend/internal/store"
)

// AdminConfig tunes gateway health for the admin list.
type AdminConfig struct {
	GatewayHealthyWindow time.Duration
}

// UserModeration is the blocked flag after an admin block or unblock.
type UserModeration struct {
	ID        string
	IsBlocked bool
}

// LocationState is a location after an admin activation change.
// Existing trips are not rewritten.
type LocationState struct {
	ID       string
	Type     string
	IsActive bool
}

// GatewayHealth is one SMS gateway without its API key.
type GatewayHealth struct {
	ID               string
	Name             string
	IsActive         bool
	Healthy          bool
	LastHeartbeatAt  *time.Time
	OperatorPrefixes []string
	CreatedAt        time.Time
}

// SMSStats counts the queue by status.
type SMSStats struct {
	Pending    int
	Processing int
	Sent       int
	Failed     int
}

// Admin is the /admin/v1 service besides gateway registration.
type Admin struct {
	pool     *pgxpool.Pool
	q        *store.Queries
	notifier notify.Notifier
	window   time.Duration
	logger   *slog.Logger
	now      func() time.Time
}

// NewAdmin wires the admin service. notifier may be nil.
func NewAdmin(pool *pgxpool.Pool, notifier notify.Notifier, cfg AdminConfig, logger *slog.Logger) *Admin {
	if logger == nil {
		logger = slog.Default()
	}
	if notifier == nil {
		notifier = notify.Nop{Logger: logger}
	}
	if cfg.GatewayHealthyWindow <= 0 {
		cfg.GatewayHealthyWindow = 30 * time.Second
	}
	return &Admin{
		pool: pool, q: store.New(pool), notifier: notifier,
		window: cfg.GatewayHealthyWindow, logger: logger, now: time.Now,
	}
}

// ListReports returns reports, optionally filtered by status.
func (s *Admin) ListReports(ctx context.Context, status string, q PageQuery) (ReportPage, error) {
	var filter pgtype.Text
	if status != "" {
		if !knownReportStatus(status) {
			return ReportPage{}, validationError(map[string]string{"status": "oneof"})
		}
		filter = pgtype.Text{String: status, Valid: true}
	}
	page, size, limit, offset, err := parsePage(q.Page, q.PageSize)
	if err != nil {
		return ReportPage{}, err
	}
	rows, err := s.q.ListReports(ctx, store.ListReportsParams{
		StatusFilter: filter,
		PageOffset:   int32(offset),
		PageLimit:    int32(limit),
	})
	if err != nil {
		return ReportPage{}, fmt.Errorf("list reports: %w", err)
	}
	trimmed, more := trimPage(rows, size)
	out := make([]Report, 0, len(trimmed))
	for _, row := range trimmed {
		out = append(out, reportFrom(row))
	}
	return ReportPage{Reports: out, Page: page, PageSize: size, HasMore: more}, nil
}

// UpdateReportStatus sets a report's status. Unknown statuses are rejected.
func (s *Admin) UpdateReportStatus(ctx context.Context, id, status string) (Report, error) {
	reportID, err := parseUUID(id)
	if err != nil {
		return Report{}, ErrNotFound
	}
	if !knownReportStatus(status) {
		return Report{}, validationError(map[string]string{"status": "oneof"})
	}
	row, err := s.q.UpdateReportStatus(ctx, store.UpdateReportStatusParams{ID: reportID, Status: status})
	if errors.Is(err, pgx.ErrNoRows) {
		return Report{}, ErrNotFound
	}
	if err != nil {
		return Report{}, fmt.Errorf("update report: %w", err)
	}
	s.logger.Info("report status updated", "report_id", id, "status", status)
	return reportFrom(row), nil
}

// BlockUser sets is_blocked, revokes every refresh token, and runs the same
// trip and booking cancellation as account deletion. The profile is not
// anonymized, and vehicles are removed because that cascade deletes them.
// Unblocking does not restore trips, bookings, vehicles, or tokens.
func (s *Admin) BlockUser(ctx context.Context, id string) (UserModeration, error) {
	return s.setBlocked(ctx, id, true)
}

// UnblockUser clears is_blocked. Cancelled trips and bookings stay cancelled.
func (s *Admin) UnblockUser(ctx context.Context, id string) (UserModeration, error) {
	return s.setBlocked(ctx, id, false)
}

func (s *Admin) setBlocked(ctx context.Context, id string, blocked bool) (UserModeration, error) {
	userID, err := parseUUID(id)
	if err != nil {
		return UserModeration{}, ErrNotFound
	}
	err = store.RunInTx(ctx, s.pool, func(q *store.Queries) error {
		user, err := q.GetUserByIDForUpdate(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && user.DeletedAt.Valid) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock user: %w", err)
		}
		n, err := q.SetUserBlocked(ctx, store.SetUserBlockedParams{ID: userID, IsBlocked: blocked})
		if err != nil {
			return fmt.Errorf("set blocked: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		if !blocked {
			return nil
		}
		if _, err := q.RevokeAllUserRefreshTokens(ctx, userID); err != nil {
			return fmt.Errorf("revoke refresh tokens: %w", err)
		}
		events, err := ReleaseUserActivity(ctx, q, userID)
		if err != nil {
			return err
		}
		if err := s.notifier.Notify(store.WithQueries(ctx, q), events...); err != nil {
			return fmt.Errorf("notify: %w", err)
		}
		return nil
	})
	if err != nil {
		return UserModeration{}, err
	}
	s.logger.Info("admin user block", "user_id", id, "is_blocked", blocked)
	return UserModeration{ID: userID.String(), IsBlocked: blocked}, nil
}

// SetLocationActive flips is_active. Trips that already use the location are
// left unchanged; new trips cannot select an inactive location.
func (s *Admin) SetLocationActive(ctx context.Context, id string, active bool) (LocationState, error) {
	locID, err := parseUUID(id)
	if err != nil {
		return LocationState{}, ErrNotFound
	}
	row, err := s.q.SetLocationActive(ctx, store.SetLocationActiveParams{ID: locID, IsActive: active})
	if errors.Is(err, pgx.ErrNoRows) {
		return LocationState{}, ErrNotFound
	}
	if err != nil {
		return LocationState{}, fmt.Errorf("set location active: %w", err)
	}
	s.logger.Info("location active", "location_id", id, "is_active", active)
	return LocationState{ID: row.ID.String(), Type: string(row.Type), IsActive: row.IsActive}, nil
}

// Gateways lists every gateway and whether it heartbeated inside the healthy window.
func (s *Admin) Gateways(ctx context.Context) ([]GatewayHealth, error) {
	rows, err := s.q.ListSMSGateways(ctx)
	if err != nil {
		return nil, fmt.Errorf("list gateways: %w", err)
	}
	now := s.clock()
	out := make([]GatewayHealth, 0, len(rows))
	for _, row := range rows {
		prefixes := row.OperatorPrefixes
		if prefixes == nil {
			prefixes = []string{}
		}
		item := GatewayHealth{
			ID:               row.ID.String(),
			Name:             row.Name,
			IsActive:         row.IsActive,
			OperatorPrefixes: prefixes,
			CreatedAt:        row.CreatedAt.Time,
			Healthy:          gatewayHealthy(row.IsActive, row.LastHeartbeatAt, now, s.window),
		}
		if row.LastHeartbeatAt.Valid {
			t := row.LastHeartbeatAt.Time
			item.LastHeartbeatAt = &t
		}
		out = append(out, item)
	}
	return out, nil
}

// SMSStats returns pending, processing, sent, and failed counts.
func (s *Admin) SMSStats(ctx context.Context) (SMSStats, error) {
	row, err := s.q.SMSQueueStats(ctx)
	if err != nil {
		return SMSStats{}, fmt.Errorf("sms stats: %w", err)
	}
	return SMSStats{
		Pending:    int(row.Pending),
		Processing: int(row.Processing),
		Sent:       int(row.Sent),
		Failed:     int(row.Failed),
	}, nil
}

func (s *Admin) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

func gatewayHealthy(active bool, last pgtype.Timestamptz, now time.Time, window time.Duration) bool {
	if !active || !last.Valid {
		return false
	}
	return !last.Time.Before(now.Add(-window))
}
