package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type queriesContextKey struct{}

// WithQueries attaches the transaction-scoped queries so a Notifier can insert
// rows on the same transaction. The inbox reads them back with QueriesFromContext.
func WithQueries(ctx context.Context, q *Queries) context.Context {
	return context.WithValue(ctx, queriesContextKey{}, q)
}

// QueriesFromContext returns queries bound by WithQueries.
func QueriesFromContext(ctx context.Context) (*Queries, bool) {
	q, ok := ctx.Value(queriesContextKey{}).(*Queries)
	return q, ok && q != nil
}

// RunInTx runs fn inside a transaction. The callback receives queries bound
// to that transaction. Rollback is issued if fn returns an error or panics.
func RunInTx(ctx context.Context, pool *pgxpool.Pool, fn func(*Queries) error) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := fn(New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
