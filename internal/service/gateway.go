package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shalarasgar/carpool-backend/internal/auth"
	"github.com/shalarasgar/carpool-backend/internal/store"
)

// GatewayConfig tunes SMS claiming and retries.
type GatewayConfig struct {
	LockDuration time.Duration
	MaxAttempts  int
	MaxBatch     int
}

// Gateway serves the Android SMS gateway app and the admin gateway endpoint.
type Gateway struct {
	q      *store.Queries
	cfg    GatewayConfig
	logger *slog.Logger
}

// NewGateway wires the gateway service. logger may be nil.
func NewGateway(pool *pgxpool.Pool, cfg GatewayConfig, logger *slog.Logger) *Gateway {
	if logger == nil {
		logger = slog.Default()
	}
	return &Gateway{q: store.New(pool), cfg: cfg, logger: logger}
}

// GatewayIdentity is an authenticated gateway.
type GatewayIdentity struct {
	ID   pgtype.UUID
	Name string
}

// QueuedSMS is a message handed to a gateway for delivery.
type QueuedSMS struct {
	ID          string
	Phone       string
	Message     string
	Attempts    int
	LockedUntil time.Time
	CreatedAt   time.Time
}

// ReportStatus is what a gateway reports after trying to send.
type ReportStatus string

// Report statuses.
const (
	ReportSent   ReportStatus = "sent"
	ReportFailed ReportStatus = "failed"
)

// ReportResult is the queue state after a report.
type ReportResult struct {
	Status   string
	Attempts int
}

// CreatedGateway is returned once; the plain API key is never stored.
type CreatedGateway struct {
	ID               string
	Name             string
	APIKey           string
	OperatorPrefixes []string
	CreatedAt        time.Time
}

// Authenticate resolves an API key to an active gateway.
func (g *Gateway) Authenticate(ctx context.Context, apiKey string) (GatewayIdentity, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return GatewayIdentity{}, ErrUnauthorized
	}
	row, err := g.q.GetSMSGatewayByKeyHash(ctx, auth.HashSecret(apiKey))
	if errors.Is(err, pgx.ErrNoRows) {
		return GatewayIdentity{}, ErrUnauthorized
	}
	if err != nil {
		return GatewayIdentity{}, fmt.Errorf("load gateway: %w", err)
	}
	return GatewayIdentity{ID: row.ID, Name: row.Name}, nil
}

// ClaimPending atomically claims up to limit pending messages for gw and
// records a heartbeat. limit is clamped to 1..MaxBatch.
func (g *Gateway) ClaimPending(ctx context.Context, gw GatewayIdentity, limit int) ([]QueuedSMS, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > g.cfg.MaxBatch {
		limit = g.cfg.MaxBatch
	}
	if err := g.q.TouchSMSGatewayHeartbeat(ctx, gw.ID); err != nil {
		return nil, fmt.Errorf("heartbeat: %w", err)
	}
	rows, err := g.q.ClaimPendingSMS(ctx, store.ClaimPendingSMSParams{
		GatewayID:   gw.ID,
		BatchSize:   int32(limit),
		LockSeconds: int32(g.cfg.LockDuration / time.Second),
	})
	if err != nil {
		return nil, fmt.Errorf("claim sms: %w", err)
	}
	out := make([]QueuedSMS, 0, len(rows))
	for _, r := range rows {
		out = append(out, QueuedSMS{
			ID:          r.ID.String(),
			Phone:       r.Phone,
			Message:     r.Message,
			Attempts:    int(r.Attempts),
			LockedUntil: r.LockedUntil.Time,
			CreatedAt:   r.CreatedAt.Time,
		})
	}
	if len(out) > 0 {
		g.logger.Info("sms claimed", "gateway", gw.Name, "count", len(out))
	}
	return out, nil
}

// Report records a delivery result for a message claimed by gw.
func (g *Gateway) Report(ctx context.Context, gw GatewayIdentity, smsID string, status ReportStatus, errText string) (ReportResult, error) {
	var id pgtype.UUID
	if err := id.Scan(smsID); err != nil {
		return ReportResult{}, ErrSMSNotFound
	}
	if err := g.q.TouchSMSGatewayHeartbeat(ctx, gw.ID); err != nil {
		return ReportResult{}, fmt.Errorf("heartbeat: %w", err)
	}

	switch status {
	case ReportSent:
		n, err := g.q.MarkSMSSent(ctx, store.MarkSMSSentParams{ID: id, GatewayID: gw.ID})
		if err != nil {
			return ReportResult{}, fmt.Errorf("mark sms sent: %w", err)
		}
		if n == 0 {
			return ReportResult{}, ErrSMSNotFound
		}
		g.logger.Info("sms sent", "sms_id", smsID, "gateway", gw.Name)
		return ReportResult{Status: string(store.SmsStatusSent)}, nil

	case ReportFailed:
		errText = strings.TrimSpace(errText)
		if len(errText) > 500 {
			errText = errText[:500]
		}
		row, err := g.q.MarkSMSFailed(ctx, store.MarkSMSFailedParams{
			ID:          id,
			GatewayID:   gw.ID,
			LastError:   pgtype.Text{String: errText, Valid: errText != ""},
			MaxAttempts: int32(g.cfg.MaxAttempts),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ReportResult{}, ErrSMSNotFound
		}
		if err != nil {
			return ReportResult{}, fmt.Errorf("mark sms failed: %w", err)
		}
		g.logger.Warn("sms delivery failed", "sms_id", smsID, "gateway", gw.Name, "attempts", row.Attempts, "status", row.Status)
		return ReportResult{Status: string(row.Status), Attempts: int(row.Attempts)}, nil

	default:
		return ReportResult{}, ErrValidation
	}
}

// Heartbeat records that gw is alive.
func (g *Gateway) Heartbeat(ctx context.Context, gw GatewayIdentity) error {
	if err := g.q.TouchSMSGatewayHeartbeat(ctx, gw.ID); err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	return nil
}

// Create registers a gateway and returns its API key. prefixes are digit
// strings matched against the national number (the part after +993).
func (g *Gateway) Create(ctx context.Context, name string, prefixes []string) (CreatedGateway, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return CreatedGateway{}, ErrValidation
	}
	clean := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if len(p) > 8 || strings.Trim(p, "0123456789") != "" {
			return CreatedGateway{}, ErrValidation
		}
		clean = append(clean, p)
	}

	key, err := auth.NewAPIKey()
	if err != nil {
		return CreatedGateway{}, err
	}
	row, err := g.q.CreateSMSGateway(ctx, store.CreateSMSGatewayParams{
		Name:             name,
		ApiKeyHash:       auth.HashSecret(key),
		OperatorPrefixes: clean,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return CreatedGateway{}, ErrGatewayExists
		}
		return CreatedGateway{}, fmt.Errorf("create gateway: %w", err)
	}
	g.logger.Info("sms gateway created", "gateway_id", row.ID.String(), "name", row.Name)
	return CreatedGateway{
		ID:               row.ID.String(),
		Name:             row.Name,
		APIKey:           key,
		OperatorPrefixes: row.OperatorPrefixes,
		CreatedAt:        row.CreatedAt.Time,
	}, nil
}
