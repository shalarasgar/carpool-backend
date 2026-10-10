package service

import (
	"context"
	"log/slog"
	"time"
)

// smsGatewayHealthyWindow matches the gateway heartbeat window in the spec.
const smsGatewayHealthyWindow = 30 * time.Second

// ReadinessStore is the storage needed to decide if the process can serve traffic.
type ReadinessStore interface {
	Ping(ctx context.Context) error
	HasHealthySMSGateway(ctx context.Context, since time.Time) (bool, error)
}

// Status is the readiness result. SMSGatewayHealthy is informational:
// a missing or stale gateway does not fail the check.
type Status struct {
	Database          string
	SMSGatewayHealthy bool
}

// Health evaluates liveness dependencies.
type Health struct {
	store  ReadinessStore
	logger *slog.Logger
	now    func() time.Time
	window time.Duration
}

// NewHealth returns a readiness checker. logger may be nil.
func NewHealth(store ReadinessStore, logger *slog.Logger) *Health {
	if logger == nil {
		logger = slog.Default()
	}
	return &Health{store: store, logger: logger, now: time.Now, window: smsGatewayHealthyWindow}
}

// WithGatewayWindow overrides how recent a gateway heartbeat must be. It
// should match SMS_GATEWAY_HEALTHY_WINDOW so /readyz agrees with routing.
func (h *Health) WithGatewayWindow(window time.Duration) *Health {
	if window > 0 {
		h.window = window
	}
	return h
}

// CheckReady pings the database. A database error fails the check.
// An SMS gateway query error is logged and reported as unhealthy.
func (h *Health) CheckReady(ctx context.Context) (Status, error) {
	if err := h.store.Ping(ctx); err != nil {
		return Status{Database: "down", SMSGatewayHealthy: false}, err
	}

	since := h.now().Add(-h.window)
	healthy, err := h.store.HasHealthySMSGateway(ctx, since)
	if err != nil {
		h.logger.Warn("sms gateway health check failed", "err", err)
		return Status{Database: "up", SMSGatewayHealthy: false}, nil
	}
	return Status{Database: "up", SMSGatewayHealthy: healthy}, nil
}
