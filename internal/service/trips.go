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

// Trips implements trip create, search, update and driver lifecycle.
type Trips struct {
	rideDB
}

// NewTrips wires the trip service. notifier may be nil, which uses a no-op.
func NewTrips(pool *pgxpool.Pool, notifier notify.Notifier, logger *slog.Logger) *Trips {
	return &Trips{rideDB: newRideDB(pool, notifier, logger)}
}

// Create publishes a trip. departs_at must be in the future, seats are 1..8,
// the endpoints differ and are active, and the vehicle, if any, belongs to
// the driver.
func (s *Trips) Create(ctx context.Context, driverID string, in TripInput, locale string) (Trip, error) {
	driver, err := parseUserID(driverID)
	if err != nil {
		return Trip{}, err
	}
	if in.SeatsTotal < minTripSeats || in.SeatsTotal > maxTripSeats {
		return Trip{}, validationError(map[string]string{"seats_total": "min"})
	}
	if err := requireFuture(in.DepartsAt, s.clock()); err != nil {
		return Trip{}, err
	}
	currency, err := normalizeCurrency(in.Currency)
	if err != nil {
		return Trip{}, err
	}
	price, err := priceOrNull(in.SuggestedPrice)
	if err != nil {
		return Trip{}, err
	}
	pickup, err := optionalNote(in.PickupNote, "pickup_note")
	if err != nil {
		return Trip{}, err
	}
	dropoff, err := optionalNote(in.DropoffNote, "dropoff_note")
	if err != nil {
		return Trip{}, err
	}
	note, err := optionalNote(in.Note, "note")
	if err != nil {
		return Trip{}, err
	}
	locale = localeOrDefault(locale)

	var created store.Trip
	err = store.RunInTx(ctx, s.pool, func(q *store.Queries) error {
		if err := requireActiveUser(ctx, q, driver); err != nil {
			return err
		}
		fromID, err := requireActiveLocation(ctx, q, in.FromLocationID, "from_location_id")
		if err != nil {
			return err
		}
		toID, err := requireActiveLocation(ctx, q, in.ToLocationID, "to_location_id")
		if err != nil {
			return err
		}
		if fromID.String() == toID.String() {
			return validationError(map[string]string{"from_location_id": "same", "to_location_id": "same"})
		}
		var vehicle pgtype.UUID
		if in.VehicleID != nil {
			vehicle, err = requireDriverVehicle(ctx, q, driver, *in.VehicleID)
			if err != nil {
				return err
			}
		}
		created, err = q.CreateTrip(ctx, store.CreateTripParams{
			DriverID:       driver,
			VehicleID:      vehicle,
			FromLocationID: fromID,
			ToLocationID:   toID,
			DepartsAt:      timestamptz(in.DepartsAt),
			SeatsTotal:     int32(in.SeatsTotal),
			SuggestedPrice: price,
			Currency:       currency,
			PickupNote:     pickup,
			DropoffNote:    dropoff,
			Note:           note,
		})
		if err != nil {
			return fmt.Errorf("create trip: %w", err)
		}
		return nil
	})
	if err != nil {
		return Trip{}, err
	}
	s.logger.Info("trip created", "user_id", driverID, "trip_id", created.ID.String())
	return loadTrip(ctx, s.q, created.ID, locale)
}

// Search lists bookable trips. A welayat id expands to itself, when active,
// and every active descendant. Cancelled, full, past and seatless trips are
// excluded, as are trips of a driver blocked in either direction.
func (s *Trips) Search(ctx context.Context, viewerID string, q TripSearch) (TripPage, error) {
	viewer, err := parseUserID(viewerID)
	if err != nil {
		return TripPage{}, err
	}
	page, size, limit, offset, err := parsePage(q.Page, q.PageSize)
	if err != nil {
		return TripPage{}, err
	}
	minSeats, err := parseMinSeats(q.Seats)
	if err != nil {
		return TripPage{}, err
	}
	from, until, err := searchDay(q.Date)
	if err != nil {
		return TripPage{}, err
	}
	fromID, err := lookupLocation(ctx, s.q, q.From, "from")
	if err != nil {
		return TripPage{}, err
	}
	toID, err := lookupLocation(ctx, s.q, q.To, "to")
	if err != nil {
		return TripPage{}, err
	}
	locale := localeOrDefault(q.Locale)
	var departFrom, departUntil pgtype.Timestamptz
	if !from.IsZero() {
		departFrom = timestamptz(from)
		departUntil = timestamptz(until)
	}
	rows, err := s.q.SearchTrips(ctx, store.SearchTripsParams{
		MinSeats:    int32(minSeats),
		ViewerID:    viewer,
		FromID:      fromID,
		ToID:        toID,
		DepartFrom:  departFrom,
		DepartUntil: departUntil,
		PageOffset:  int32(offset),
		PageLimit:   int32(limit),
	})
	if err != nil {
		return TripPage{}, fmt.Errorf("search trips: %w", err)
	}
	trimmed, more := trimPage(rows, size)
	out := make([]Trip, 0, len(trimmed))
	for _, row := range trimmed {
		trip, err := tripFromSource(sourceFromSearch(row), locale)
		if err != nil {
			return TripPage{}, err
		}
		out = append(out, trip)
	}
	return TripPage{Trips: out, Page: page, PageSize: size, HasMore: more}, nil
}

// Get returns one trip. A viewer who is blocked in either direction, and who
// is not the driver, gets TRIP_NOT_FOUND. The payload has no phone number.
func (s *Trips) Get(ctx context.Context, viewerID, tripID, locale string) (Trip, error) {
	viewer, err := parseUserID(viewerID)
	if err != nil {
		return Trip{}, err
	}
	id, err := parseUUID(tripID)
	if err != nil {
		return Trip{}, ErrTripNotFound
	}
	trip, err := loadTrip(ctx, s.q, id, localeOrDefault(locale))
	if err != nil {
		return Trip{}, err
	}
	if sameID(trip.Driver.ID, viewer.String()) {
		return trip, nil
	}
	blocked, err := blockedEitherWay(ctx, s.q, viewer, mustUUID(trip.Driver.ID))
	if err != nil {
		return Trip{}, err
	}
	if blocked {
		return Trip{}, ErrTripNotFound
	}
	return trip, nil
}

// Update changes the driver's trip while it is active or full. seats_total
// cannot drop below the seats already approved. seats_left and status move
// with that change: zero seats left makes the trip full, and freeing a seat
// on a full trip makes it active.
func (s *Trips) Update(ctx context.Context, driverID, tripID string, patch TripPatch, locale string) (Trip, error) {
	if patch.empty() {
		return Trip{}, validationError(map[string]string{"body": "empty"})
	}
	driver, err := parseUserID(driverID)
	if err != nil {
		return Trip{}, err
	}
	id, err := parseUUID(tripID)
	if err != nil {
		return Trip{}, ErrTripNotFound
	}
	locale = localeOrDefault(locale)
	err = store.RunInTx(ctx, s.pool, func(q *store.Queries) error {
		if err := requireActiveUser(ctx, q, driver); err != nil {
			return err
		}
		row, err := q.GetTripForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTripNotFound
		}
		if err != nil {
			return fmt.Errorf("lock trip: %w", err)
		}
		if !sameID(row.DriverID.String(), driver.String()) {
			return ErrForbidden
		}
		if row.Status != store.TripStatusActive && row.Status != store.TripStatusFull {
			return ErrInvalidState
		}
		next := row
		if patch.FromLocationID != nil || patch.ToLocationID != nil {
			fromRaw := row.FromLocationID.String()
			toRaw := row.ToLocationID.String()
			if patch.FromLocationID != nil {
				fromRaw = *patch.FromLocationID
			}
			if patch.ToLocationID != nil {
				toRaw = *patch.ToLocationID
			}
			fromID, err := requireActiveLocation(ctx, q, fromRaw, "from_location_id")
			if err != nil {
				return err
			}
			toID, err := requireActiveLocation(ctx, q, toRaw, "to_location_id")
			if err != nil {
				return err
			}
			if fromID.String() == toID.String() {
				return validationError(map[string]string{"from_location_id": "same", "to_location_id": "same"})
			}
			next.FromLocationID = fromID
			next.ToLocationID = toID
		}
		if patch.Vehicle.Set {
			if patch.Vehicle.Value == nil || *patch.Vehicle.Value == "" {
				next.VehicleID = pgtype.UUID{}
			} else {
				vehicle, err := requireDriverVehicle(ctx, q, driver, *patch.Vehicle.Value)
				if err != nil {
					return err
				}
				next.VehicleID = vehicle
			}
		}
		if patch.DepartsAt != nil {
			if err := requireFuture(*patch.DepartsAt, s.clock()); err != nil {
				return err
			}
			next.DepartsAt = timestamptz(*patch.DepartsAt)
		}
		if patch.SeatsTotal != nil {
			approved, err := q.SumApprovedSeats(ctx, id)
			if err != nil {
				return fmt.Errorf("sum approved seats: %w", err)
			}
			if int(approved) != int(row.SeatsTotal-row.SeatsLeft) {
				return ErrInvalidState
			}
			total, left, status, err := resizeTrip(string(row.Status), int(row.SeatsTotal), int(row.SeatsLeft), *patch.SeatsTotal)
			if err != nil {
				if errors.Is(err, errInconsistentSeats) {
					return ErrInvalidState
				}
				return err
			}
			next.SeatsTotal = int32(total)
			next.SeatsLeft = int32(left)
			next.Status = store.TripStatus(status)
		}
		if patch.SuggestedPrice.Set {
			if patch.SuggestedPrice.Value == nil {
				next.SuggestedPrice = pgtype.Numeric{}
			} else {
				price, err := parsePrice(*patch.SuggestedPrice.Value)
				if err != nil {
					return err
				}
				next.SuggestedPrice = price
			}
		}
		if patch.Currency != nil {
			currency, err := normalizeCurrency(*patch.Currency)
			if err != nil {
				return err
			}
			next.Currency = currency
		}
		if patch.PickupNote != nil {
			note, err := optionalNote(patch.PickupNote, "pickup_note")
			if err != nil {
				return err
			}
			next.PickupNote = note
		}
		if patch.DropoffNote != nil {
			note, err := optionalNote(patch.DropoffNote, "dropoff_note")
			if err != nil {
				return err
			}
			next.DropoffNote = note
		}
		if patch.Note != nil {
			note, err := optionalNote(patch.Note, "note")
			if err != nil {
				return err
			}
			next.Note = note
		}
		if _, err := q.SaveTrip(ctx, store.SaveTripParams{
			ID:             next.ID,
			VehicleID:      next.VehicleID,
			FromLocationID: next.FromLocationID,
			ToLocationID:   next.ToLocationID,
			DepartsAt:      next.DepartsAt,
			SeatsTotal:     next.SeatsTotal,
			SeatsLeft:      next.SeatsLeft,
			SuggestedPrice: next.SuggestedPrice,
			Currency:       next.Currency,
			PickupNote:     next.PickupNote,
			DropoffNote:    next.DropoffNote,
			Note:           next.Note,
			Status:         next.Status,
		}); err != nil {
			return fmt.Errorf("save trip: %w", err)
		}
		return nil
	})
	if err != nil {
		return Trip{}, err
	}
	s.logger.Info("trip updated", "user_id", driverID, "trip_id", tripID)
	return loadTrip(ctx, s.q, id, locale)
}

// Cancel marks the driver's active or full trip cancelled and cancels every
// pending or approved booking in the same transaction. Repeating it is a no-op
// and does not notify again. Each passenger receives trip_cancelled.
func (s *Trips) Cancel(ctx context.Context, driverID, tripID, locale string) (Trip, error) {
	driver, err := parseUserID(driverID)
	if err != nil {
		return Trip{}, err
	}
	id, err := parseUUID(tripID)
	if err != nil {
		return Trip{}, ErrTripNotFound
	}
	var events []notify.Event
	err = store.RunInTx(ctx, s.pool, func(q *store.Queries) error {
		if err := requireActiveUser(ctx, q, driver); err != nil {
			return err
		}
		row, err := q.GetTripForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTripNotFound
		}
		if err != nil {
			return fmt.Errorf("lock trip: %w", err)
		}
		if !sameID(row.DriverID.String(), driver.String()) {
			return ErrForbidden
		}
		changed, err := cancelTrip(string(row.Status))
		if err != nil || !changed {
			return err
		}
		cancelled, err := q.CancelLiveBookingsForTrip(ctx, id)
		if err != nil {
			return fmt.Errorf("cancel bookings: %w", err)
		}
		n, err := q.SetTripStatus(ctx, store.SetTripStatusParams{ID: id, Status: store.TripStatusCancelled})
		if err != nil {
			return fmt.Errorf("cancel trip: %w", err)
		}
		if n == 0 {
			return ErrTripNotFound
		}
		events = make([]notify.Event, 0, len(cancelled))
		for _, booking := range cancelled {
			events = append(events, notify.Event{
				Kind:        notify.KindTripCancelled,
				RecipientID: booking.PassengerID.String(),
				ActorID:     driver.String(),
				TripID:      id.String(),
				BookingID:   booking.ID.String(),
			})
		}
		return nil
	})
	if err != nil {
		return Trip{}, err
	}
	s.notify(ctx, events)
	s.logger.Info("trip cancelled", "user_id", driverID, "trip_id", tripID, "bookings", len(events))
	return loadTrip(ctx, s.q, id, localeOrDefault(locale))
}

// Complete marks the driver's trip completed once departs_at has been reached.
// Bookings are left as they are. Repeating it is a no-op.
func (s *Trips) Complete(ctx context.Context, driverID, tripID, locale string) (Trip, error) {
	driver, err := parseUserID(driverID)
	if err != nil {
		return Trip{}, err
	}
	id, err := parseUUID(tripID)
	if err != nil {
		return Trip{}, ErrTripNotFound
	}
	err = store.RunInTx(ctx, s.pool, func(q *store.Queries) error {
		if err := requireActiveUser(ctx, q, driver); err != nil {
			return err
		}
		row, err := q.GetTripForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTripNotFound
		}
		if err != nil {
			return fmt.Errorf("lock trip: %w", err)
		}
		if !sameID(row.DriverID.String(), driver.String()) {
			return ErrForbidden
		}
		_, changed, err := completeTrip(string(row.Status), row.DepartsAt.Time, s.clock())
		if err != nil || !changed {
			return err
		}
		n, err := q.SetTripStatus(ctx, store.SetTripStatusParams{ID: id, Status: store.TripStatusCompleted})
		if err != nil {
			return fmt.Errorf("complete trip: %w", err)
		}
		if n == 0 {
			return ErrTripNotFound
		}
		return nil
	})
	if err != nil {
		return Trip{}, err
	}
	s.logger.Info("trip completed", "user_id", driverID, "trip_id", tripID)
	return loadTrip(ctx, s.q, id, localeOrDefault(locale))
}

// ListMine returns the driver's trips, newest departure first.
func (s *Trips) ListMine(ctx context.Context, driverID string, q TripList) (TripPage, error) {
	driver, err := parseUserID(driverID)
	if err != nil {
		return TripPage{}, err
	}
	page, size, limit, offset, err := parsePage(q.Page, q.PageSize)
	if err != nil {
		return TripPage{}, err
	}
	status, err := parseTripStatusFilter(q.Status)
	if err != nil {
		return TripPage{}, err
	}
	rows, err := s.q.ListDriverTrips(ctx, store.ListDriverTripsParams{
		DriverID:   driver,
		Status:     status,
		PageOffset: int32(offset),
		PageLimit:  int32(limit),
	})
	if err != nil {
		return TripPage{}, fmt.Errorf("list trips: %w", err)
	}
	trimmed, more := trimPage(rows, size)
	locale := localeOrDefault(q.Locale)
	out := make([]Trip, 0, len(trimmed))
	for _, row := range trimmed {
		trip, err := tripFromSource(sourceFromDriver(row), locale)
		if err != nil {
			return TripPage{}, err
		}
		out = append(out, trip)
	}
	return TripPage{Trips: out, Page: page, PageSize: size, HasMore: more}, nil
}

func requireFuture(t time.Time, now time.Time) error {
	if t.IsZero() || !t.After(now) {
		return validationError(map[string]string{"departs_at": "future"})
	}
	return nil
}

func priceOrNull(raw *string) (pgtype.Numeric, error) {
	if raw == nil || *raw == "" {
		return pgtype.Numeric{}, nil
	}
	return parsePrice(*raw)
}

func parseTripStatusFilter(raw string) (pgtype.Text, error) {
	switch raw {
	case "":
		return pgtype.Text{}, nil
	case tripActive, tripFull, tripCancelled, tripCompleted, tripExpired:
		return pgtype.Text{String: raw, Valid: true}, nil
	default:
		return pgtype.Text{}, validationError(map[string]string{"status": "oneof"})
	}
}

func mustUUID(raw string) pgtype.UUID {
	id, err := parseUUID(raw)
	if err != nil {
		return pgtype.UUID{}
	}
	return id
}
