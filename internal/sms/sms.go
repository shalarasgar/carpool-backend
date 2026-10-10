// Package sms defines the outbound SMS provider interface and the queue-backed
// implementation that the Android gateway app drains.
package sms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shalarasgar/carpool-backend/internal/auth"
	"github.com/shalarasgar/carpool-backend/internal/store"
)

// Provider sends one text message. Implementations must not log the text,
// because OTP codes travel through it.
type Provider interface {
	Send(ctx context.Context, phone, text string) error
}

// QueueProvider inserts messages into sms_queue. Delivery happens when a
// gateway polls GET /gateway/v1/pending.
type QueueProvider struct {
	q             *store.Queries
	logger        *slog.Logger
	healthyWindow time.Duration
	now           func() time.Time
}

// NewQueueProvider returns a provider writing to the pool. healthyWindow is
// how recent a gateway heartbeat must be for the gateway to receive routing.
func NewQueueProvider(pool *pgxpool.Pool, healthyWindow time.Duration, logger *slog.Logger) *QueueProvider {
	if logger == nil {
		logger = slog.Default()
	}
	return &QueueProvider{q: store.New(pool), logger: logger, healthyWindow: healthyWindow, now: time.Now}
}

// Send enqueues the message. A healthy gateway is pre-selected when one
// exists (operator-prefix routing first, then any healthy gateway). With no
// healthy gateway the row is still inserted with gateway_id NULL and a
// warning is logged; the first gateway to poll will claim it.
func (p *QueueProvider) Send(ctx context.Context, phone, text string) error {
	gatewayID, err := p.q.SelectSMSGatewayForPhone(ctx, store.SelectSMSGatewayForPhoneParams{
		Since:          pgtype.Timestamptz{Time: p.now().Add(-p.healthyWindow), Valid: true},
		NationalNumber: strings.TrimPrefix(phone, "+993"),
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		p.logger.Warn("no healthy sms gateway; message queued unassigned", "phone", auth.MaskPhone(phone))
		gatewayID = pgtype.UUID{}
	case err != nil:
		return fmt.Errorf("select sms gateway: %w", err)
	}

	id, err := p.q.EnqueueSMS(ctx, store.EnqueueSMSParams{Phone: phone, Message: text, GatewayID: gatewayID})
	if err != nil {
		return fmt.Errorf("enqueue sms: %w", err)
	}
	p.logger.Info("sms queued", "sms_id", id.String(), "phone", auth.MaskPhone(phone), "gateway_assigned", gatewayID.Valid)
	return nil
}

// NoopProvider drops messages. Use it with DEV_MODE=true when no gateway is
// available; the OTP code is then only visible in the API response.
type NoopProvider struct {
	logger *slog.Logger
}

// NewNoopProvider returns a provider that only logs the masked recipient.
func NewNoopProvider(logger *slog.Logger) *NoopProvider {
	if logger == nil {
		logger = slog.Default()
	}
	return &NoopProvider{logger: logger}
}

// Send implements Provider.
func (p *NoopProvider) Send(_ context.Context, phone, _ string) error {
	p.logger.Info("sms dropped by noop provider", "phone", auth.MaskPhone(phone))
	return nil
}
