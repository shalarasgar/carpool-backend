package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// CreateBooking stores a pending seat request. Pending requests do not hold seats, but a
// request that already cannot fit on its segment is refused.
func (db *AppDB) CreateBooking(ctx context.Context, in ride.BookingInput, now time.Time) (string, error) {
	var id string
	err := db.inTx(ctx, "create booking", func(tx pgx.Tx) error {
		t, err := lockTrip(ctx, tx, in.TripID)
		if err != nil {
			return err
		}
		if t.DriverID == in.PassengerID {
			return ride.ErrOwnTrip
		}
		if !t.Status.Bookable() {
			return ride.ErrTripNotActive
		}
		if !t.DepartsAt.After(now) {
			return ride.ErrTripStarted
		}
		var first, last string
		if err := tx.QueryRow(ctx, `
			SELECT first_name, last_name FROM users WHERE id = $1::uuid AND NOT is_blocked AND deleted_at IS NULL
		`, in.PassengerID).Scan(&first, &last); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ride.ErrNotFound
			}
			return dbErr("load passenger", err)
		}
		if first == "" || last == "" {
			return ride.ErrProfileIncomplete
		}
		blocked, err := blockedEitherWay(ctx, tx, in.PassengerID, t.DriverID)
		if err != nil {
			return err
		}
		if blocked {
			return ride.ErrUserBlocked
		}
		if err := ride.ValidateSegment(t.Stops, in.PickupOrder, in.DropoffOrder); err != nil {
			return err
		}
		if !ride.Fits(t.SeatsTotal, t.Occupants, len(t.Stops), in.PickupOrder, in.DropoffOrder, in.Seats) {
			return ride.ErrInsufficientSeats
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO bookings (trip_id, passenger_id, seats, pickup_order, dropoff_order, luggage, message)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, NULLIF($7, ''))
			RETURNING id::text
		`, in.TripID, in.PassengerID, in.Seats, in.PickupOrder, in.DropoffOrder, in.Luggage, in.Message).Scan(&id)
		if isUnique(err) {
			return ride.ErrBookingExists
		}
		if err != nil {
			return dbErr("insert booking", err)
		}
		return insertEvents(ctx, tx, []ride.Event{{
			UserID: t.DriverID, Type: ride.NoteBookingRequested, TripID: in.TripID, BookingID: id,
			ActorID: in.PassengerID, DedupeKey: "booking_requested:" + id,
		}})
	})
	return id, err
}

type lockedBooking struct {
	ID           string
	TripID       string
	PassengerID  string
	Status       ride.BookingStatus
	Seats        int
	PickupOrder  int
	DropoffOrder int
}

func bookingTripID(ctx context.Context, q dbtx, id string) (string, error) {
	var tripID string
	err := q.QueryRow(ctx, `SELECT trip_id::text FROM bookings WHERE id = $1::uuid`, id).Scan(&tripID)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
		return "", ride.ErrNotFound
	}
	if err != nil {
		return "", dbErr("booking trip", err)
	}
	return tripID, nil
}

// lockBookingWithTrip locks the trip first and the booking second. Every transaction
// that changes seats uses this order, so they cannot deadlock.
func lockBookingWithTrip(ctx context.Context, tx pgx.Tx, id string) (lockedTrip, lockedBooking, error) {
	tripID, err := bookingTripID(ctx, tx, id)
	if err != nil {
		return lockedTrip{}, lockedBooking{}, err
	}
	t, err := lockTrip(ctx, tx, tripID)
	if err != nil {
		return lockedTrip{}, lockedBooking{}, err
	}
	var b lockedBooking
	var status string
	err = tx.QueryRow(ctx, `
		SELECT id::text, trip_id::text, passenger_id::text, status::text, seats, pickup_order, dropoff_order
		FROM bookings WHERE id = $1::uuid FOR UPDATE
	`, id).Scan(&b.ID, &b.TripID, &b.PassengerID, &status, &b.Seats, &b.PickupOrder, &b.DropoffOrder)
	if err != nil {
		return lockedTrip{}, lockedBooking{}, dbErr("lock booking", err)
	}
	b.Status = ride.BookingStatus(status)
	return t, b, nil
}

// DecideBooking approves or rejects a pending booking for the trip's driver.
//
// Approval runs under the trip row lock: the approved occupants are re-read after the
// lock is taken, and the booking is approved only if its seats fit on every segment of
// its route part. A competing approval waits for the lock and then sees this one.
// Repeating the same decision returns the booking unchanged.
func (db *AppDB) DecideBooking(ctx context.Context, driverID, bookingID string, approve bool, now time.Time) error {
	return db.inTx(ctx, "decide booking", func(tx pgx.Tx) error {
		t, b, err := lockBookingWithTrip(ctx, tx, bookingID)
		if err != nil {
			return err
		}
		if t.DriverID != driverID {
			if b.PassengerID == driverID {
				return ride.ErrForbidden
			}
			return ride.ErrNotFound
		}
		target := ride.BookingRejected
		if approve {
			target = ride.BookingApproved
		}
		if b.Status == target {
			return nil
		}
		if b.Status != ride.BookingPending {
			return ride.ErrBookingNotPending
		}
		if !t.Status.Bookable() {
			return ride.ErrTripNotActive
		}
		if !t.DepartsAt.After(now) {
			return ride.ErrTripStarted
		}
		if approve && !ride.Fits(t.SeatsTotal, t.Occupants, len(t.Stops), b.PickupOrder, b.DropoffOrder, b.Seats) {
			return ride.ErrInsufficientSeats
		}
		if _, err := tx.Exec(ctx, `
			UPDATE bookings SET status = $2::booking_status, decided_at = $3 WHERE id = $1::uuid
		`, b.ID, target, now); err != nil {
			return dbErr("decide booking", err)
		}
		note := ride.NoteBookingRejected
		if approve {
			note = ride.NoteBookingApproved
			occ := append(append([]ride.Occupant(nil), t.Occupants...), ride.Occupant{From: b.PickupOrder, To: b.DropoffOrder, Seats: b.Seats})
			if err := refreshTripStatus(ctx, tx, t, occ); err != nil {
				return err
			}
		}
		return insertEvents(ctx, tx, []ride.Event{{
			UserID: b.PassengerID, Type: note, TripID: t.ID, BookingID: b.ID, ActorID: driverID,
			DedupeKey: string(note) + ":" + b.ID,
		}})
	})
}

// CancelBooking cancels a pending or approved booking for its passenger or the driver.
// An approved booking can only be cancelled before departure. Because availability is
// computed from approved bookings, leaving the approved state frees the seats exactly
// once; a repeated cancel finds the booking already cancelled and changes nothing.
func (db *AppDB) CancelBooking(ctx context.Context, actorID, bookingID string, now time.Time) error {
	return db.inTx(ctx, "cancel booking", func(tx pgx.Tx) error {
		t, b, err := lockBookingWithTrip(ctx, tx, bookingID)
		if err != nil {
			return err
		}
		return cancelLockedBooking(ctx, tx, t, b, actorID, now)
	})
}

// cancelLockedBooking cancels b for actorID, who must be its passenger or the driver of t.
// The caller holds the trip and booking locks.
func cancelLockedBooking(ctx context.Context, tx pgx.Tx, t lockedTrip, b lockedBooking, actorID string, now time.Time) error {
	var target ride.BookingStatus
	var notify string
	switch actorID {
	case b.PassengerID:
		target, notify = ride.BookingCancelledByPassenger, t.DriverID
	case t.DriverID:
		target, notify = ride.BookingCancelledByDriver, b.PassengerID
	default:
		return ride.ErrNotFound
	}
	if b.Status.Cancelled() {
		return nil
	}
	if !ride.CanTransitionBooking(b.Status, target) {
		return ride.ErrInvalidTransition
	}
	if b.Status == ride.BookingApproved && !t.DepartsAt.After(now) {
		return ride.ErrTripStarted
	}
	if _, err := tx.Exec(ctx, `
		UPDATE bookings SET status = $2::booking_status, cancelled_at = $3 WHERE id = $1::uuid
	`, b.ID, target, now); err != nil {
		return dbErr("cancel booking", err)
	}
	if b.Status == ride.BookingApproved {
		remaining := make([]ride.Occupant, 0, len(t.Occupants))
		skipped := false
		for _, o := range t.Occupants {
			if !skipped && o.From == b.PickupOrder && o.To == b.DropoffOrder && o.Seats == b.Seats {
				skipped = true
				continue
			}
			remaining = append(remaining, o)
		}
		if err := refreshTripStatus(ctx, tx, t, remaining); err != nil {
			return err
		}
	}
	return insertEvents(ctx, tx, []ride.Event{{
		UserID: notify, Type: ride.NotificationType("booking_" + string(target)), TripID: t.ID, BookingID: b.ID,
		ActorID: actorID, DedupeKey: string(target) + ":" + b.ID,
	}})
}

const bookingSelect = `
SELECT b.id::text, b.trip_id::text, b.status::text, b.seats, b.pickup_order, b.dropoff_order, b.luggage,
       COALESCE(b.message, ''), b.created_at, b.decided_at, b.cancelled_at, b.completed_at,
       ` + summarySelect + `,
       t.driver_id::text, COALESCE(u.phone, ''), COALESCE(du.phone, ''),
       EXISTS (SELECT 1 FROM reviews r WHERE r.booking_id = b.id AND r.from_user_id = $1::uuid)
FROM bookings b
JOIN users u ON u.id = b.passenger_id
JOIN trips t ON t.id = b.trip_id
JOIN users du ON du.id = t.driver_id`

// scanBooking scans a booking row for viewerID. The counterpart phone is kept only when
// the viewer is a participant and the status allows contact; otherwise it is dropped here,
// before any response is built.
func scanBooking(row pgx.Row, viewerID string) (ride.Booking, string, error) {
	var b ride.Booking
	var status, driverID, passengerPhone, driverPhone string
	dest := []any{&b.ID, &b.TripID, &status, &b.Seats, &b.PickupOrder, &b.DropoffOrder, &b.Luggage,
		&b.Message, &b.CreatedAt, &b.DecidedAt, &b.CancelledAt, &b.CompletedAt}
	dest = append(dest, scanSummary(&b.Passenger)...)
	dest = append(dest, &driverID, &passengerPhone, &driverPhone, &b.Reviewed)
	if err := row.Scan(dest...); err != nil {
		return ride.Booking{}, "", err
	}
	b.Status = ride.BookingStatus(status)
	if b.Status.ContactVisible() {
		switch viewerID {
		case b.Passenger.ID:
			b.ContactPhone = driverPhone
		case driverID:
			b.ContactPhone = passengerPhone
		}
	}
	return b, driverID, nil
}

func (db *AppDB) queryBookings(ctx context.Context, viewerID, where string, args ...any) ([]ride.Booking, error) {
	rows, err := db.pool.Query(ctx, bookingSelect+" "+where, append([]any{viewerID}, args...)...)
	if err != nil {
		return nil, dbErr("query bookings", err)
	}
	defer rows.Close()
	out := []ride.Booking{}
	for rows.Next() {
		b, _, err := scanBooking(rows, viewerID)
		if err != nil {
			return nil, dbErr("scan booking", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (db *AppDB) attachTrips(ctx context.Context, bookings []ride.Booking, viewerID string) error {
	seen := map[string]bool{}
	var ids []string
	for _, b := range bookings {
		if !seen[b.TripID] {
			seen[b.TripID] = true
			ids = append(ids, b.TripID)
		}
	}
	trips, err := loadTrips(ctx, db.pool, ids, viewerID)
	if err != nil {
		return err
	}
	byID := map[string]*ride.Trip{}
	for i := range trips {
		byID[trips[i].ID] = &trips[i]
	}
	for i := range bookings {
		bookings[i].Trip = byID[bookings[i].TripID]
	}
	return nil
}

// Booking loads one booking for a participant. Anyone else gets ErrNotFound.
func (db *AppDB) Booking(ctx context.Context, viewerID, id string) (ride.Booking, error) {
	b, driverID, err := scanBooking(db.pool.QueryRow(ctx, bookingSelect+` WHERE b.id = $2::uuid`, viewerID, id), viewerID)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
		return ride.Booking{}, ride.ErrNotFound
	}
	if err != nil {
		return ride.Booking{}, dbErr("load booking", err)
	}
	if viewerID != b.Passenger.ID && viewerID != driverID {
		return ride.Booking{}, ride.ErrNotFound
	}
	list := []ride.Booking{b}
	if err := db.attachTrips(ctx, list, viewerID); err != nil {
		return ride.Booking{}, err
	}
	return list[0], nil
}

// BookingContact returns the counterpart's phone for an approved or completed booking.
func (db *AppDB) BookingContact(ctx context.Context, viewerID, id string) (string, error) {
	b, err := db.Booking(ctx, viewerID, id)
	if err != nil {
		return "", err
	}
	if !b.Status.ContactVisible() || b.ContactPhone == "" {
		return "", ride.ErrContactUnavailable
	}
	return b.ContactPhone, nil
}

// TripBookings lists the bookings of a trip for its driver, newest first.
func (db *AppDB) TripBookings(ctx context.Context, driverID, tripID string) ([]ride.Booking, error) {
	var owner string
	err := db.pool.QueryRow(ctx, `SELECT driver_id::text FROM trips WHERE id = $1::uuid`, tripID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
		return nil, ride.ErrNotFound
	}
	if err != nil {
		return nil, dbErr("trip owner", err)
	}
	if owner != driverID {
		return nil, ride.ErrForbidden
	}
	return db.queryBookings(ctx, driverID, `WHERE b.trip_id = $2::uuid ORDER BY
		CASE b.status WHEN 'pending' THEN 0 WHEN 'approved' THEN 1 ELSE 2 END, b.created_at DESC`, tripID)
}

// PassengerBookings lists the passenger's bookings. scope is "upcoming" or "history".
func (db *AppDB) PassengerBookings(ctx context.Context, passengerID, scope string, page, limit int) ([]ride.Booking, bool, error) {
	cond := `b.status IN ('pending', 'approved') AND t.status IN ('active', 'full', 'in_progress')`
	order := "t.departs_at ASC"
	if scope == "history" {
		cond = `NOT (` + cond + `)`
		order = "t.departs_at DESC"
	}
	list, err := db.queryBookings(ctx, passengerID, `WHERE b.passenger_id = $1::uuid AND `+cond+`
		ORDER BY `+order+`, b.created_at DESC LIMIT $2 OFFSET $3`, limit+1, (page-1)*limit)
	if err != nil {
		return nil, false, err
	}
	more := len(list) > limit
	if more {
		list = list[:limit]
	}
	if err := db.attachTrips(ctx, list, passengerID); err != nil {
		return nil, false, err
	}
	return list, more, nil
}
