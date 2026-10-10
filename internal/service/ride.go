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

// rideDB is the shared database access for trips and bookings.
// Mutating transactions lock the acting user before any trip row, and the
// trip row before any booking row, so they cannot deadlock with each other
// or with account deletion.
type rideDB struct {
	pool     *pgxpool.Pool
	q        *store.Queries
	notifier notify.Notifier
	logger   *slog.Logger
	now      func() time.Time
}

func newRideDB(pool *pgxpool.Pool, notifier notify.Notifier, logger *slog.Logger) rideDB {
	if logger == nil {
		logger = slog.Default()
	}
	if notifier == nil {
		notifier = notify.Nop{Logger: logger}
	}
	return rideDB{
		pool:     pool,
		q:        store.New(pool),
		notifier: notifier,
		logger:   logger,
		now:      time.Now,
	}
}

func (r rideDB) clock() time.Time {
	if r.now == nil {
		return time.Now()
	}
	return r.now()
}

func (r rideDB) notify(ctx context.Context, events []notify.Event) {
	if r.notifier == nil || len(events) == 0 {
		return
	}
	if err := r.notifier.Notify(ctx, events...); err != nil {
		r.logger.Error("notify failed", "err", err)
	}
}

// requireActiveUser locks the user row. Deleted and blocked accounts cannot
// mutate trips or bookings. Call this before locking a trip.
func requireActiveUser(ctx context.Context, q *store.Queries, id pgtype.UUID) error {
	user, err := q.GetUserByIDForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return fmt.Errorf("lock user: %w", err)
	}
	if user.DeletedAt.Valid {
		return ErrUnauthorized
	}
	if user.IsBlocked {
		return ErrUserBlocked
	}
	return nil
}

func requireActiveLocation(ctx context.Context, q *store.Queries, raw, field string) (pgtype.UUID, error) {
	id, err := parseUUID(raw)
	if err != nil {
		return pgtype.UUID{}, validationError(map[string]string{field: "uuid"})
	}
	loc, err := q.GetLocationByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, validationError(map[string]string{field: "not_found"})
	}
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("load location: %w", err)
	}
	if !loc.IsActive {
		return pgtype.UUID{}, validationError(map[string]string{field: "inactive"})
	}
	return id, nil
}

func lookupLocation(ctx context.Context, q *store.Queries, raw, field string) (pgtype.UUID, error) {
	if raw == "" {
		return pgtype.UUID{}, nil
	}
	id, err := parseUUID(raw)
	if err != nil {
		return pgtype.UUID{}, validationError(map[string]string{field: "uuid"})
	}
	_, err = q.GetLocationByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, validationError(map[string]string{field: "not_found"})
	}
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("load location: %w", err)
	}
	return id, nil
}

func requireDriverVehicle(ctx context.Context, q *store.Queries, driver pgtype.UUID, raw string) (pgtype.UUID, error) {
	if raw == "" {
		return pgtype.UUID{}, nil
	}
	id, err := parseUUID(raw)
	if err != nil {
		return pgtype.UUID{}, validationError(map[string]string{"vehicle_id": "uuid"})
	}
	row, err := q.GetVehicleByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !sameID(row.UserID.String(), driver.String())) {
		return pgtype.UUID{}, validationError(map[string]string{"vehicle_id": "owner"})
	}
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("load vehicle: %w", err)
	}
	return id, nil
}

func blockedEitherWay(ctx context.Context, q *store.Queries, a, b pgtype.UUID) (bool, error) {
	if a.String() == b.String() {
		return false, nil
	}
	blocked, err := q.BlockExistsEitherWay(ctx, store.BlockExistsEitherWayParams{
		BlockerID: a,
		BlockedID: b,
	})
	if err != nil {
		return false, fmt.Errorf("check block: %w", err)
	}
	return blocked, nil
}

func loadTrip(ctx context.Context, q *store.Queries, id pgtype.UUID, locale string) (Trip, error) {
	row, err := q.GetTripView(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Trip{}, ErrTripNotFound
	}
	if err != nil {
		return Trip{}, fmt.Errorf("load trip: %w", err)
	}
	return tripFromSource(sourceFromView(row), locale)
}
