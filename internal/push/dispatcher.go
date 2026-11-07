package push

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// Store is the outbox the dispatcher drains.
type Store interface {
	ClaimPushes(ctx context.Context, now time.Time, limit int) ([]ride.PushJob, error)
	DeletePushTokens(ctx context.Context, tokens []string) error
	PrunePushTokens(ctx context.Context, before time.Time) error
}

// Sender delivers one message to one device token.
type Sender interface {
	Send(ctx context.Context, token string, m Message) error
}

// Config is the delivery policy.
type Config struct {
	// Title is the visible title of every push, usually the app name.
	Title string
	// MaxAge skips notifications older than this (a backlog after downtime). Zero sends all.
	MaxAge time.Duration
	// TokenTTL removes tokens that the app did not refresh for this long. Zero keeps them.
	TokenTTL time.Duration
	// Batch is how many notifications one claim takes.
	Batch int
}

// Dispatcher sends a push for every new inbox row. It implements jobs.Maintainer, so
// the existing reaper loop runs it.
type Dispatcher struct {
	store  Store
	sender Sender
	cfg    Config
	logger *slog.Logger
	now    func() time.Time
	pruned time.Time
}

// NewDispatcher returns a dispatcher. A non-positive batch becomes 100.
func NewDispatcher(store Store, sender Sender, cfg Config, logger *slog.Logger) *Dispatcher {
	if cfg.Batch <= 0 {
		cfg.Batch = 100
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{store: store, sender: sender, cfg: cfg, logger: logger, now: time.Now}
}

// maxRounds bounds one Maintain call so a large backlog does not starve shutdown.
const maxRounds = 10

// Maintain drains the outbox in batches and prunes stale tokens once an hour.
func (d *Dispatcher) Maintain(ctx context.Context) error {
	now := d.now()
	if d.cfg.TokenTTL > 0 && now.Sub(d.pruned) >= time.Hour {
		if err := d.store.PrunePushTokens(ctx, now.Add(-d.cfg.TokenTTL)); err != nil {
			return err
		}
		d.pruned = now
	}
	for range maxRounds {
		jobs, err := d.store.ClaimPushes(ctx, now, d.cfg.Batch)
		if err != nil {
			return err
		}
		var dead []string
		for _, j := range jobs {
			dead = append(dead, d.deliver(ctx, now, j)...)
		}
		if err := d.store.DeletePushTokens(ctx, dead); err != nil {
			return err
		}
		if len(jobs) < d.cfg.Batch || ctx.Err() != nil {
			return nil
		}
		now = d.now()
	}
	return nil
}

// deliver sends j to each of its tokens and returns the tokens FCM rejected for good.
func (d *Dispatcher) deliver(ctx context.Context, now time.Time, j ride.PushJob) []string {
	if len(j.Tokens) == 0 || (d.cfg.MaxAge > 0 && now.Sub(j.CreatedAt) > d.cfg.MaxAge) {
		return nil
	}
	msg, ok := Build(d.cfg.Title, j)
	if !ok {
		return nil
	}
	var dead []string
	for _, token := range j.Tokens {
		err := d.sender.Send(ctx, token, msg)
		switch {
		case err == nil:
		case errors.Is(err, ErrInvalidToken):
			dead = append(dead, token)
		default:
			// Tokens and texts stay out of logs.
			d.logger.Warn("push failed", "notification_id", j.NotificationID, "type", string(j.Type), "err", err)
		}
	}
	return dead
}
