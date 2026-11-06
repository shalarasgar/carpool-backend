package service

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/shalarasgar/carpool-backend/internal/auth"
	"github.com/shalarasgar/carpool-backend/internal/smsqueue"
)

// GatewayStore is the persistence used by the phone gateway.
type GatewayStore interface {
	GatewayByHash(ctx context.Context, hash string) (smsqueue.Gateway, error)
	TouchHeartbeat(ctx context.Context, id string, now time.Time) error
	ClaimPending(ctx context.Context, claimer smsqueue.Gateway, limit int, lease time.Duration, now time.Time) ([]smsqueue.Job, error)
	MutateJob(ctx context.Context, id string, fn func(*smsqueue.Job) error) error
	ExpireDue(ctx context.Context, maxAge time.Duration, now time.Time) (int, error)
	Cleanup(ctx context.Context, before time.Time) error
	CreateGateway(ctx context.Context, name, hash string, prefixes []string) (smsqueue.Gateway, error)
	ListGateways(ctx context.Context) ([]smsqueue.Gateway, error)
	RevokeGateway(ctx context.Context, id string) error
	QueueStats(ctx context.Context) (map[string]int, error)
}

// GatewayConfig controls leases, retries, and the per-gateway request quota.
type GatewayConfig struct {
	Lease         time.Duration
	SendingLease  time.Duration
	MaxAttempts   int
	MessageMaxAge time.Duration
	RatePerMinute int
}

// Gateway is the authenticated phone API: claim, commit-to-send, and report.
type Gateway struct {
	store  GatewayStore
	cfg    GatewayConfig
	limit  *rateLimiter
	logger *slog.Logger
	now    func() time.Time
}

// NewGateway returns the gateway service.
func NewGateway(store GatewayStore, cfg GatewayConfig, logger *slog.Logger) *Gateway {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Lease <= 0 {
		cfg.Lease = time.Minute
	}
	if cfg.SendingLease <= 0 {
		cfg.SendingLease = 3 * time.Minute
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.MessageMaxAge <= 0 {
		cfg.MessageMaxAge = 5 * time.Minute
	}
	return &Gateway{
		store:  store,
		cfg:    cfg,
		limit:  newRateLimiter(cfg.RatePerMinute, time.Minute),
		logger: logger,
		now:    time.Now,
	}
}

// Authenticate resolves a raw API key to an active gateway. The raw key is not logged.
func (g *Gateway) Authenticate(ctx context.Context, rawKey string) (smsqueue.Gateway, error) {
	rawKey = strings.TrimSpace(rawKey)
	if rawKey == "" {
		return smsqueue.Gateway{}, smsqueue.ErrUnauthorized
	}
	hash := auth.HashSecret(rawKey)
	if err := g.limit.allow("auth:"+hash, g.now()); err != nil {
		return smsqueue.Gateway{}, err
	}
	return g.store.GatewayByHash(ctx, hash)
}

// Heartbeat marks the gateway available for new OTP messages.
func (g *Gateway) Heartbeat(ctx context.Context, gatewayID string) error {
	if err := g.limit.allow(gatewayID, g.now()); err != nil {
		return err
	}
	return g.store.TouchHeartbeat(ctx, gatewayID, g.now())
}

// Claim returns authorized messages and acts as a heartbeat.
// The message body is the exact text the phone must send.
func (g *Gateway) Claim(ctx context.Context, claimer smsqueue.Gateway, limit int) ([]smsqueue.Job, error) {
	if err := g.limit.allow(claimer.ID, g.now()); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 10 {
		return nil, auth.ErrValidation
	}
	return g.store.ClaimPending(ctx, claimer, limit, g.cfg.Lease, g.now())
}

// MarkSending commits this gateway to the radio hand-off.
// A later crash must not offer the same job to a different phone.
func (g *Gateway) MarkSending(ctx context.Context, gatewayID, jobID string) error {
	if err := g.limit.allow(gatewayID, g.now()); err != nil {
		return err
	}
	return g.store.MutateJob(ctx, jobID, func(job *smsqueue.Job) error {
		if job.GatewayID != gatewayID {
			return smsqueue.ErrConflict
		}
		next, err := smsqueue.MarkSending(*job, g.cfg.SendingLease, g.now())
		if err != nil {
			return err
		}
		*job = next
		return nil
	})
}

// Report records telephony acceptance, a definite failure, an unknown outcome, or a carrier receipt.
func (g *Gateway) Report(ctx context.Context, gatewayID string, in smsqueue.Report) error {
	if err := g.limit.allow(gatewayID, g.now()); err != nil {
		return err
	}
	return g.store.MutateJob(ctx, in.ID, func(job *smsqueue.Job) error {
		if job.GatewayID != gatewayID {
			return smsqueue.ErrConflict
		}
		next, err := smsqueue.ApplyReport(*job, in, g.cfg.MaxAttempts, g.now())
		if err != nil {
			return err
		}
		*job = next
		return nil
	})
}

// Maintain expires leases and deletes old terminal rows.
func (g *Gateway) Maintain(ctx context.Context) error {
	n, err := g.store.ExpireDue(ctx, g.cfg.MessageMaxAge, g.now())
	if err != nil {
		return err
	}
	if n > 0 {
		g.logger.Info("sms queue maintenance", "changed", n)
	}
	return g.store.Cleanup(ctx, g.now().Add(-7*24*time.Hour))
}

// CreateGateway issues a new API key. The raw key is returned once and only its hash is stored.
func (g *Gateway) CreateGateway(ctx context.Context, name string, prefixes []string) (smsqueue.Gateway, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return smsqueue.Gateway{}, "", auth.ErrValidation
	}
	clean := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" {
			continue
		}
		if len(prefix) > 8 {
			return smsqueue.Gateway{}, "", auth.ErrValidation
		}
		for _, r := range prefix {
			if !unicode.IsDigit(r) {
				return smsqueue.Gateway{}, "", auth.ErrValidation
			}
		}
		clean = append(clean, prefix)
	}
	raw, hash, err := auth.NewToken("gw_")
	if err != nil {
		return smsqueue.Gateway{}, "", err
	}
	gateway, err := g.store.CreateGateway(ctx, name, hash, clean)
	if err != nil {
		return smsqueue.Gateway{}, "", err
	}
	return gateway, raw, nil
}

// Gateways lists credentials without their API keys.
func (g *Gateway) Gateways(ctx context.Context) ([]smsqueue.Gateway, error) {
	return g.store.ListGateways(ctx)
}

// RevokeGateway disables a phone. It does not delete historical jobs.
func (g *Gateway) RevokeGateway(ctx context.Context, id string) error {
	return g.store.RevokeGateway(ctx, id)
}

// Stats returns queue counts and gateway rows.
func (g *Gateway) Stats(ctx context.Context) (map[string]int, []smsqueue.Gateway, error) {
	counts, err := g.store.QueueStats(ctx)
	if err != nil {
		return nil, nil, err
	}
	gateways, err := g.store.ListGateways(ctx)
	if err != nil {
		return nil, nil, err
	}
	return counts, gateways, nil
}

type rateLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	if limit <= 0 {
		limit = 120
	}
	return &rateLimiter{hits: map[string][]time.Time{}, limit: limit, window: window}
}

func (l *rateLimiter) allow(id string, now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-l.window)
	kept := l.hits[id][:0]
	for _, at := range l.hits[id] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) >= l.limit {
		l.hits[id] = kept
		retry := l.window
		if len(kept) > 0 {
			retry = kept[0].Add(l.window).Sub(now)
		}
		if retry < time.Second {
			retry = time.Second
		}
		return &smsqueue.RateLimited{RetryAfter: retry}
	}
	l.hits[id] = append(kept, now)
	return nil
}
