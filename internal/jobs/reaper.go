// Package jobs runs the SMS lease reaper.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Maintainer expires SMS leases and deletes old terminal rows.
type Maintainer interface {
	Maintain(ctx context.Context) error
}

// Reaper calls Maintain until the context is cancelled.
type Reaper struct {
	maintainer Maintainer
	interval   time.Duration
	logger     *slog.Logger
}

// NewReaper returns a reaper. A non-positive interval becomes 15 seconds.
func NewReaper(maintainer Maintainer, interval time.Duration, logger *slog.Logger) *Reaper {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Reaper{maintainer: maintainer, interval: interval, logger: logger}
}

// Run blocks until ctx is cancelled. Maintenance errors are logged and retried.
func (r *Reaper) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	r.once(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.once(ctx)
		}
	}
}

func (r *Reaper) once(ctx context.Context) {
	if err := r.maintainer.Maintain(ctx); err != nil {
		r.logger.Error("maintenance failed", "err_type", errType(err), "err", err)
	}
}

func errType(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%T", err)
}
