package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Readiness is the database side of the readiness check.
type Readiness struct {
	pool *pgxpool.Pool
	q    *Queries
}

// NewReadiness binds readiness queries to the pool.
func NewReadiness(pool *pgxpool.Pool) *Readiness {
	return &Readiness{pool: pool, q: New(pool)}
}

// Ping reports whether the pool can reach PostgreSQL.
func (r *Readiness) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}

// HasHealthySMSGateway reports whether any active gateway heartbeated after since.
func (r *Readiness) HasHealthySMSGateway(ctx context.Context, since time.Time) (bool, error) {
	return r.q.HasHealthySMSGateway(ctx, pgtype.Timestamptz{Time: since, Valid: true})
}
