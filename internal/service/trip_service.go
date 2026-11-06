package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// TripStore is the persistence of trips. Transactions and locks live behind it.
type TripStore interface {
	CreateTrip(ctx context.Context, in ride.TripInput, buffer time.Duration) (string, error)
	UpdateTrip(ctx context.Context, driverID, tripID string, p ride.TripPatch, now time.Time, buffer time.Duration) error
	CancelTrip(ctx context.Context, driverID, tripID string, now time.Time) error
	CompleteTrip(ctx context.Context, driverID, tripID string, now time.Time) error
	Trip(ctx context.Context, id, viewerID string) (ride.Trip, error)
	SearchTrips(ctx context.Context, q ride.SearchQuery, now time.Time, tz string) ([]ride.Trip, bool, error)
	DriverTrips(ctx context.Context, driverID, scope string, page, limit int) ([]ride.Trip, bool, error)
	TripLifecycle(ctx context.Context, now time.Time, grace, remindBefore time.Duration) error
}

// TripConfig is the trip policy.
type TripConfig struct {
	// OverlapBuffer is the minimum gap between two trips of one driver.
	OverlapBuffer time.Duration
	// TimeZone is the IANA zone of search dates, for example Asia/Ashgabat.
	TimeZone string
	// CompleteGrace is how long after the planned arrival a trip completes automatically.
	CompleteGrace time.Duration
	// RemindBefore is how long before departure participants get a reminder.
	RemindBefore time.Duration
}

// Trips publishes, searches, edits, and closes trips.
type Trips struct {
	store  TripStore
	cfg    TripConfig
	loc    *time.Location
	logger *slog.Logger
	now    func() time.Time
}

// NewTrips returns the trip service. An unknown time zone falls back to UTC+5.
func NewTrips(store TripStore, cfg TripConfig, logger *slog.Logger) *Trips {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.TimeZone == "" {
		cfg.TimeZone = "Asia/Ashgabat"
	}
	loc, err := time.LoadLocation(cfg.TimeZone)
	if err != nil {
		logger.Warn("time zone not found, using UTC+5", "tz", cfg.TimeZone)
		loc = time.FixedZone("UTC+5", 5*3600)
	}
	return &Trips{store: store, cfg: cfg, loc: loc, logger: logger, now: time.Now}
}

// Location is the service time zone used for local dates.
func (s *Trips) Location() *time.Location { return s.loc }

// Publish validates and creates a trip for the driver.
func (s *Trips) Publish(ctx context.Context, in ride.TripInput) (ride.Trip, error) {
	if err := in.Normalize(s.now()); err != nil {
		return ride.Trip{}, err
	}
	id, err := s.store.CreateTrip(ctx, in, s.cfg.OverlapBuffer)
	if err != nil {
		return ride.Trip{}, err
	}
	return s.store.Trip(ctx, id, in.DriverID)
}

// Get returns a trip for a viewer.
func (s *Trips) Get(ctx context.Context, id, viewerID string) (ride.Trip, error) {
	if !ride.ValidID(id) {
		return ride.Trip{}, ride.ErrNotFound
	}
	return s.store.Trip(ctx, id, viewerID)
}

// Update applies the driver's patch.
func (s *Trips) Update(ctx context.Context, driverID, id string, p ride.TripPatch) (ride.Trip, error) {
	if !ride.ValidID(id) {
		return ride.Trip{}, ride.ErrNotFound
	}
	if p.VehicleID != nil && !ride.ValidID(*p.VehicleID) {
		return ride.Trip{}, &ride.FieldError{Field: "vehicle_id", Reason: "not_found"}
	}
	if err := p.Normalize(); err != nil {
		return ride.Trip{}, err
	}
	if err := s.store.UpdateTrip(ctx, driverID, id, p, s.now(), s.cfg.OverlapBuffer); err != nil {
		return ride.Trip{}, err
	}
	return s.store.Trip(ctx, id, driverID)
}

// Cancel cancels a trip and its open bookings.
func (s *Trips) Cancel(ctx context.Context, driverID, id string) (ride.Trip, error) {
	if !ride.ValidID(id) {
		return ride.Trip{}, ride.ErrNotFound
	}
	if err := s.store.CancelTrip(ctx, driverID, id, s.now()); err != nil {
		return ride.Trip{}, err
	}
	return s.store.Trip(ctx, id, driverID)
}

// Complete closes a departed trip.
func (s *Trips) Complete(ctx context.Context, driverID, id string) (ride.Trip, error) {
	if !ride.ValidID(id) {
		return ride.Trip{}, ride.ErrNotFound
	}
	if err := s.store.CompleteTrip(ctx, driverID, id, s.now()); err != nil {
		return ride.Trip{}, err
	}
	return s.store.Trip(ctx, id, driverID)
}

// Search finds trips that fit the query.
func (s *Trips) Search(ctx context.Context, q ride.SearchQuery) ([]ride.Trip, bool, error) {
	if (q.FromID != "" && !ride.ValidID(q.FromID)) || (q.ToID != "" && !ride.ValidID(q.ToID)) {
		return nil, false, ride.ErrLocationInactive
	}
	if err := q.Normalize(); err != nil {
		return nil, false, err
	}
	return s.store.SearchTrips(ctx, q, s.now(), s.loc.String())
}

// Mine lists the driver's trips. scope is "upcoming" or "history".
func (s *Trips) Mine(ctx context.Context, driverID, scope string, page, limit int) ([]ride.Trip, bool, error) {
	page, limit = pageBounds(page, limit)
	return s.store.DriverTrips(ctx, driverID, scope, page, limit)
}

// Maintain advances trip lifecycles. It implements jobs.Maintainer.
func (s *Trips) Maintain(ctx context.Context) error {
	return s.store.TripLifecycle(ctx, s.now(), s.cfg.CompleteGrace, s.cfg.RemindBefore)
}

func pageBounds(page, limit int) (int, int) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	return page, limit
}
