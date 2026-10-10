// Package jobs runs periodic maintenance: returning stuck SMS to the queue,
// deleting expired OTP codes and refresh tokens, and closing trips whose
// departure time has passed.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shalarasgar/carpool-backend/internal/store"
)

// Config tunes the maintenance runner.
type Config struct {
	// Interval between runs.
	Interval time.Duration
	// GatewayHealthyWindow: pending messages assigned to a gateway silent for
	// longer than this are unassigned so any gateway can claim them.
	GatewayHealthyWindow time.Duration
	// OTPRetention keeps expired codes briefly so verify-otp can still answer
	// OTP_EXPIRED instead of OTP_INVALID.
	OTPRetention time.Duration
	// RevokedTokenRetention keeps revoked refresh tokens so reuse is detected.
	RevokedTokenRetention time.Duration
}

// Runner executes the maintenance queries.
type Runner struct {
	q      *store.Queries
	cfg    Config
	logger *slog.Logger
	now    func() time.Time
}

// New returns a runner. Zero config fields get safe defaults.
func New(pool *pgxpool.Pool, cfg Config, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.GatewayHealthyWindow <= 0 {
		cfg.GatewayHealthyWindow = 30 * time.Second
	}
	if cfg.OTPRetention <= 0 {
		cfg.OTPRetention = time.Hour
	}
	if cfg.RevokedTokenRetention <= 0 {
		cfg.RevokedTokenRetention = 7 * 24 * time.Hour
	}
	return &Runner{q: store.New(pool), cfg: cfg, logger: logger, now: time.Now}
}

// Run executes RunOnce immediately and then every Interval until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	r.runLogged(ctx)
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runLogged(ctx)
		}
	}
}

// Stats counts what one run changed.
type Stats struct {
	RequeuedSMS       int64
	UnassignedSMS     int64
	DeletedOTPs       int64
	DeletedRefresh    int64
	CompletedTrips    int64
	ExpiredTrips      int64
	InconsistentTrips int64
}

// RunOnce executes every maintenance query once. Each query is independent;
// the first error stops the run and is returned.
func (r *Runner) RunOnce(ctx context.Context) (Stats, error) {
	var s Stats
	var err error
	now := r.now()

	if s.RequeuedSMS, err = r.q.RequeueStuckSMS(ctx); err != nil {
		return s, fmt.Errorf("requeue stuck sms: %w", err)
	}
	stale := pgtype.Timestamptz{Time: now.Add(-r.cfg.GatewayHealthyWindow), Valid: true}
	if s.UnassignedSMS, err = r.q.UnassignSMSFromStaleGateways(ctx, stale); err != nil {
		return s, fmt.Errorf("unassign stale gateway sms: %w", err)
	}
	otpBefore := pgtype.Timestamptz{Time: now.Add(-r.cfg.OTPRetention), Valid: true}
	if s.DeletedOTPs, err = r.q.DeleteExpiredOTPs(ctx, otpBefore); err != nil {
		return s, fmt.Errorf("delete expired otps: %w", err)
	}
	revokedBefore := pgtype.Timestamptz{Time: now.Add(-r.cfg.RevokedTokenRetention), Valid: true}
	if s.DeletedRefresh, err = r.q.DeleteExpiredRefreshTokens(ctx, revokedBefore); err != nil {
		return s, fmt.Errorf("delete expired refresh tokens: %w", err)
	}
	// Past active/full trips with a consistent approved-seat total become
	// completed. Past active/full trips with no approved booking become
	// expired. Booking rows are not modified. A past trip that has approved
	// bookings but whose seats_left does not equal seats_total minus those
	// seats is left unchanged and counted here.
	if s.CompletedTrips, err = r.q.CompleteConsistentPastTrips(ctx); err != nil {
		return s, fmt.Errorf("complete past trips: %w", err)
	}
	if s.ExpiredTrips, err = r.q.ExpirePastTripsWithoutApproval(ctx); err != nil {
		return s, fmt.Errorf("expire past trips: %w", err)
	}
	inconsistent, err := r.q.CountInconsistentPastTrips(ctx)
	if err != nil {
		return s, fmt.Errorf("count inconsistent trips: %w", err)
	}
	s.InconsistentTrips = int64(inconsistent)
	if inconsistent > 0 {
		r.logger.Warn("past trips left unchanged because approved seats do not match seats_left", "trips", inconsistent)
	}
	return s, nil
}

func (r *Runner) runLogged(ctx context.Context) {
	runCtx, cancel := context.WithTimeout(ctx, r.cfg.Interval)
	defer cancel()
	stats, err := r.RunOnce(runCtx)
	if err != nil {
		if ctx.Err() == nil {
			r.logger.Error("maintenance job failed", "err", err)
		}
		return
	}
	if stats != (Stats{}) {
		r.logger.Info("maintenance job",
			"requeued_sms", stats.RequeuedSMS,
			"unassigned_sms", stats.UnassignedSMS,
			"deleted_otps", stats.DeletedOTPs,
			"deleted_refresh_tokens", stats.DeletedRefresh,
			"completed_trips", stats.CompletedTrips,
			"expired_trips", stats.ExpiredTrips,
			"inconsistent_trips", stats.InconsistentTrips,
		)
	}
}
