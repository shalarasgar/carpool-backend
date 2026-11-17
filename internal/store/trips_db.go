package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shalarasgar/carpool-backend/internal/ride"
)

const tripSelect = `
SELECT t.id::text, t.status::text, t.departs_at, t.arrives_at, t.seats_total, t.suggested_price::float8,
       t.price_negotiable, t.currency, COALESCE(t.note, ''), COALESCE(t.conditions, ''),
       t.air_conditioning, t.audio_system, t.music_policy, t.smoking_policy, t.pets_policy,
       t.luggage_size, t.child_seat, t.conversation, t.created_at, t.updated_at,
       ` + summarySelect + `,
       v.id::text, v.brand, v.model, v.color, v.plate, v.year::int, v.vehicle_type, v.passenger_seats::int, v.photo_url
FROM trips t
JOIN users u ON u.id = t.driver_id
LEFT JOIN vehicles v ON v.id = t.vehicle_id`

func scanTrip(row pgx.Row) (ride.Trip, error) {
	var t ride.Trip
	var status string
	var vID, vBrand, vModel, vColor, vPlate, vType, vPhoto *string
	var vYear, vSeats *int
	a := &t.Amenities
	dest := []any{&t.ID, &status, &t.DepartsAt, &t.ArrivesAt, &t.SeatsTotal, &t.Price,
		&t.PriceNegotiable, &t.Currency, &t.Description, &t.Conditions,
		&a.AirConditioning, &a.AudioSystem, &a.MusicPolicy, &a.SmokingPolicy, &a.PetsPolicy,
		&a.LuggageSize, &a.ChildSeat, &a.Conversation, &t.CreatedAt, &t.UpdatedAt}
	dest = append(dest, scanSummary(&t.Driver)...)
	dest = append(dest, &vID, &vBrand, &vModel, &vColor, &vPlate, &vYear, &vType, &vSeats, &vPhoto)
	if err := row.Scan(dest...); err != nil {
		return ride.Trip{}, err
	}
	t.Status = ride.TripStatus(status)
	if vID != nil {
		t.Vehicle = &ride.Vehicle{
			ID: *vID, Brand: deref(vBrand), Model: deref(vModel), Color: deref(vColor), Plate: deref(vPlate),
			Year: vYear, Type: deref(vType), PassengerSeats: vSeats, PhotoURL: deref(vPhoto),
		}
	}
	return t, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// loadTrips loads full trips in the order of ids, with stops, free seats per segment,
// and the viewer's relation. The plate is cleared unless the viewer may see it.
func loadTrips(ctx context.Context, q dbtx, ids []string, viewerID string) ([]ride.Trip, error) {
	if len(ids) == 0 {
		return []ride.Trip{}, nil
	}
	rows, err := q.Query(ctx, tripSelect+` WHERE t.id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, dbErr("load trips", err)
	}
	byID := map[string]*ride.Trip{}
	for rows.Next() {
		t, err := scanTrip(rows)
		if err != nil {
			rows.Close()
			return nil, dbErr("scan trip", err)
		}
		byID[t.ID] = &t
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, dbErr("trips rows", err)
	}

	if err := attachStops(ctx, q, ids, byID); err != nil {
		return nil, err
	}
	occ, err := occupantsByTrip(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	if err := attachCounts(ctx, q, ids, byID); err != nil {
		return nil, err
	}
	viewerBookings := map[string][2]string{}
	if viewerID != "" {
		if viewerBookings, err = latestViewerBookings(ctx, q, ids, viewerID); err != nil {
			return nil, err
		}
	}

	out := make([]ride.Trip, 0, len(ids))
	for _, id := range ids {
		t, ok := byID[id]
		if !ok {
			continue
		}
		load := ride.SegmentLoad(occ[id], len(t.Stops))
		t.FreeBySegment = make([]int, len(load))
		for i, l := range load {
			t.FreeBySegment[i] = max(t.SeatsTotal-l, 0)
		}
		if viewerID != "" {
			t.Viewer = &ride.TripViewer{IsDriver: t.Driver.ID == viewerID}
			if b, ok := viewerBookings[id]; ok {
				t.Viewer.BookingID = b[0]
				t.Viewer.BookingStatus = ride.BookingStatus(b[1])
			}
		}
		if t.Vehicle != nil && !t.Viewer.CanSeePlate() {
			t.Vehicle.Plate = ""
		}
		if t.Viewer == nil || !t.Viewer.IsDriver {
			t.PendingRequests, t.ApprovedSeats = 0, 0
		}
		out = append(out, *t)
	}
	return out, nil
}

func attachStops(ctx context.Context, q dbtx, ids []string, byID map[string]*ride.Trip) error {
	rows, err := q.Query(ctx, `
		SELECT s.trip_id::text, s.stop_order, COALESCE(s.meeting_point, ''), s.planned_at, s.stop_type,
		       l.id::text, COALESCE(l.parent_id::text, ''), l.type::text, l.name_tk, l.name_ru, l.name_en, l.sort_order
		FROM trip_stops s
		JOIN locations l ON l.id = s.location_id
		WHERE s.trip_id = ANY($1::uuid[])
		ORDER BY s.trip_id, s.stop_order
	`, ids)
	if err != nil {
		return dbErr("load stops", err)
	}
	defer rows.Close()
	for rows.Next() {
		var tripID, stopType string
		var s ride.StopView
		l := &s.Location
		if err := rows.Scan(&tripID, &s.Order, &s.MeetingPoint, &s.PlannedAt, &stopType,
			&l.ID, &l.ParentID, &l.Type, &l.NameTK, &l.NameRU, &l.NameEN, &l.SortOrder); err != nil {
			return dbErr("scan stop", err)
		}
		s.Type = ride.StopType(stopType)
		s.LocationID = l.ID
		if t, ok := byID[tripID]; ok {
			t.Stops = append(t.Stops, s)
		}
	}
	return rows.Err()
}

func attachCounts(ctx context.Context, q dbtx, ids []string, byID map[string]*ride.Trip) error {
	rows, err := q.Query(ctx, `
		SELECT trip_id::text,
		       COUNT(*) FILTER (WHERE status = 'pending')::int,
		       COALESCE(SUM(seats) FILTER (WHERE status IN ('approved', 'completed')), 0)::int
		FROM bookings WHERE trip_id = ANY($1::uuid[]) GROUP BY trip_id
	`, ids)
	if err != nil {
		return dbErr("booking counts", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var pending, approved int
		if err := rows.Scan(&id, &pending, &approved); err != nil {
			return dbErr("scan counts", err)
		}
		if t, ok := byID[id]; ok {
			t.PendingRequests, t.ApprovedSeats = pending, approved
		}
	}
	return rows.Err()
}

// occupantsByTrip returns the approved bookings of each trip as route occupants.
// This is the only source of seat availability.
func occupantsByTrip(ctx context.Context, q dbtx, ids []string) (map[string][]ride.Occupant, error) {
	rows, err := q.Query(ctx, `
		SELECT trip_id::text, pickup_order, dropoff_order, seats
		FROM bookings WHERE trip_id = ANY($1::uuid[]) AND status = 'approved'
	`, ids)
	if err != nil {
		return nil, dbErr("load occupants", err)
	}
	defer rows.Close()
	out := map[string][]ride.Occupant{}
	for rows.Next() {
		var id string
		var o ride.Occupant
		if err := rows.Scan(&id, &o.From, &o.To, &o.Seats); err != nil {
			return nil, dbErr("scan occupant", err)
		}
		out[id] = append(out[id], o)
	}
	return out, rows.Err()
}

func latestViewerBookings(ctx context.Context, q dbtx, ids []string, viewerID string) (map[string][2]string, error) {
	rows, err := q.Query(ctx, `
		SELECT DISTINCT ON (trip_id) trip_id::text, id::text, status::text
		FROM bookings
		WHERE trip_id = ANY($1::uuid[]) AND passenger_id = $2::uuid
		ORDER BY trip_id, (status IN ('pending', 'approved', 'completed')) DESC, created_at DESC
	`, ids, viewerID)
	if err != nil {
		return nil, dbErr("viewer bookings", err)
	}
	defer rows.Close()
	out := map[string][2]string{}
	for rows.Next() {
		var tripID, id, status string
		if err := rows.Scan(&tripID, &id, &status); err != nil {
			return nil, dbErr("scan viewer booking", err)
		}
		out[tripID] = [2]string{id, status}
	}
	return out, rows.Err()
}

// Trip loads one trip for a viewer.
func (db *AppDB) Trip(ctx context.Context, id, viewerID string) (ride.Trip, error) {
	trips, err := loadTrips(ctx, db.pool, []string{id}, viewerID)
	if err != nil {
		return ride.Trip{}, err
	}
	if len(trips) == 0 {
		return ride.Trip{}, ride.ErrNotFound
	}
	return trips[0], nil
}

// lockedTrip is the state of a trip read under FOR UPDATE.
type lockedTrip struct {
	ID         string
	DriverID   string
	VehicleID  string
	Status     ride.TripStatus
	DepartsAt  time.Time
	ArrivesAt  time.Time
	SeatsTotal int
	Stops      []ride.Stop
	Occupants  []ride.Occupant
}

// lockTrip takes the row lock every seat-changing transaction serializes on, then reads
// the route and the approved occupants. Under READ COMMITTED each later statement sees
// every booking committed by the previous lock holder.
func lockTrip(ctx context.Context, tx pgx.Tx, id string) (lockedTrip, error) {
	var t lockedTrip
	var status string
	var vehicleID *string
	err := tx.QueryRow(ctx, `
		SELECT id::text, driver_id::text, vehicle_id::text, status::text, departs_at, arrives_at, seats_total
		FROM trips WHERE id = $1::uuid FOR UPDATE
	`, id).Scan(&t.ID, &t.DriverID, &vehicleID, &status, &t.DepartsAt, &t.ArrivesAt, &t.SeatsTotal)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
		return lockedTrip{}, ride.ErrNotFound
	}
	if err != nil {
		return lockedTrip{}, dbErr("lock trip", err)
	}
	t.Status = ride.TripStatus(status)
	t.VehicleID = deref(vehicleID)

	rows, err := tx.Query(ctx, `
		SELECT stop_order, location_id::text, stop_type, planned_at
		FROM trip_stops WHERE trip_id = $1::uuid ORDER BY stop_order
	`, id)
	if err != nil {
		return lockedTrip{}, dbErr("trip stops", err)
	}
	for rows.Next() {
		var s ride.Stop
		var typ string
		if err := rows.Scan(&s.Order, &s.LocationID, &typ, &s.PlannedAt); err != nil {
			rows.Close()
			return lockedTrip{}, dbErr("scan trip stop", err)
		}
		s.Type = ride.StopType(typ)
		t.Stops = append(t.Stops, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return lockedTrip{}, dbErr("trip stops rows", err)
	}
	occ, err := occupantsByTrip(ctx, tx, []string{id})
	if err != nil {
		return lockedTrip{}, err
	}
	t.Occupants = occ[id]
	return t, nil
}

// refreshTripStatus moves a bookable trip between active and full after a seat change.
func refreshTripStatus(ctx context.Context, tx pgx.Tx, t lockedTrip, occupants []ride.Occupant) error {
	next := ride.StatusAfterLoad(t.Status, t.SeatsTotal, occupants, len(t.Stops))
	if next == t.Status {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE trips SET status = $2::trip_status WHERE id = $1::uuid`, t.ID, next); err != nil {
		return dbErr("trip status", err)
	}
	return nil
}

// checkOverlap returns an OverlapError when the driver has another live trip whose
// schedule, widened by buffer, collides with [departs, arrives).
func checkOverlap(ctx context.Context, tx pgx.Tx, driverID, excludeTripID string, departs, arrives time.Time, buffer time.Duration) error {
	exclude := excludeTripID
	if exclude == "" {
		exclude = "00000000-0000-0000-0000-000000000000"
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, departs_at, arrives_at FROM trips
		WHERE driver_id = $1::uuid AND id <> $2::uuid
		  AND status IN ('active', 'full', 'in_progress')
		  AND departs_at < $3 AND arrives_at > $4
		ORDER BY departs_at
	`, driverID, exclude, arrives.Add(buffer), departs.Add(-buffer))
	if err != nil {
		return dbErr("overlap check", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var dep, arr time.Time
		if err := rows.Scan(&id, &dep, &arr); err != nil {
			return dbErr("scan overlap", err)
		}
		if ride.Overlaps(departs, arrives, dep, arr, buffer) {
			return &ride.OverlapError{TripID: id}
		}
	}
	return rows.Err()
}

func isExclusion(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23P01"
}

func checkLocations(ctx context.Context, tx pgx.Tx, stops []ride.Stop) error {
	ids := make([]string, len(stops))
	for i, s := range stops {
		ids[i] = s.LocationID
	}
	var n int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM locations l
		LEFT JOIN locations p ON p.id = l.parent_id
		WHERE l.id = ANY($1::uuid[]) AND l.is_active AND (p.id IS NULL OR p.is_active)
	`, ids).Scan(&n); err != nil {
		return dbErr("check locations", err)
	}
	if n != len(ids) {
		return ride.ErrLocationInactive
	}
	return nil
}

// CreateTrip publishes a trip. The driver's users row is locked first, so two concurrent
// requests of one driver run the overlap check one after the other. The exclusion
// constraint trips_driver_no_overlap backs the check up.
func (db *AppDB) CreateTrip(ctx context.Context, in ride.TripInput, buffer time.Duration) (string, error) {
	var id string
	err := db.inTx(ctx, "create trip", func(tx pgx.Tx) error {
		driver, err := lockProfile(ctx, tx, in.DriverID)
		if err != nil {
			return err
		}
		if !driver.Complete() {
			return ride.ErrProfileIncomplete
		}
		vehicle, err := ownedVehicle(ctx, tx, in.DriverID, in.VehicleID)
		if errors.Is(err, ride.ErrNotFound) {
			return &ride.FieldError{Field: "vehicle_id", Reason: "not_found"}
		}
		if err != nil {
			return err
		}
		if vehicle.PassengerSeats != nil && in.SeatsTotal > *vehicle.PassengerSeats {
			return &ride.FieldError{Field: "seats_total", Reason: "exceeds_vehicle"}
		}
		if err := checkLocations(ctx, tx, in.Stops); err != nil {
			return err
		}
		if err := checkOverlap(ctx, tx, in.DriverID, "", in.DepartsAt, in.ArrivesAt, buffer); err != nil {
			return err
		}
		first, last := in.Stops[0], in.Stops[len(in.Stops)-1]
		a := in.Amenities
		err = tx.QueryRow(ctx, `
			INSERT INTO trips (driver_id, vehicle_id, from_location_id, to_location_id, departs_at, arrives_at,
				seats_total, suggested_price, price_negotiable, note, conditions,
				air_conditioning, audio_system, music_policy, smoking_policy, pets_policy, luggage_size, child_seat, conversation)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, $9, NULLIF($10, ''), NULLIF($11, ''),
				$12, $13, $14, $15, $16, $17, $18, $19)
			RETURNING id::text
		`, in.DriverID, in.VehicleID, first.LocationID, last.LocationID, in.DepartsAt, in.ArrivesAt,
			in.SeatsTotal, in.Price, in.PriceNegotiable, in.Description, in.Conditions,
			a.AirConditioning, a.AudioSystem, a.MusicPolicy, a.SmokingPolicy, a.PetsPolicy, a.LuggageSize, a.ChildSeat, a.Conversation,
		).Scan(&id)
		if isExclusion(err) {
			return &ride.OverlapError{}
		}
		if err != nil {
			return dbErr("insert trip", err)
		}
		for _, s := range in.Stops {
			if _, err := tx.Exec(ctx, `
				INSERT INTO trip_stops (trip_id, location_id, stop_order, meeting_point, planned_at, stop_type)
				VALUES ($1::uuid, $2::uuid, $3, NULLIF($4, ''), $5, $6)
			`, id, s.LocationID, s.Order, s.MeetingPoint, optTime(s.PlannedAt), string(s.Type)); err != nil {
				return dbErr("insert stop", err)
			}
		}
		return nil
	})
	return id, err
}

// UpdateTrip applies a driver's patch. Seats cannot drop below the approved load of any
// segment; the schedule can only move while no pending or approved booking exists.
func (db *AppDB) UpdateTrip(ctx context.Context, driverID, tripID string, p ride.TripPatch, now time.Time, buffer time.Duration) error {
	return db.inTx(ctx, "update trip", func(tx pgx.Tx) error {
		if _, err := lockProfile(ctx, tx, driverID); err != nil {
			return err
		}
		t, err := lockTrip(ctx, tx, tripID)
		if err != nil {
			return err
		}
		if t.DriverID != driverID {
			return ride.ErrForbidden
		}
		if !t.Status.Bookable() {
			return ride.ErrTripNotActive
		}
		if !t.DepartsAt.After(now) {
			return ride.ErrTripStarted
		}

		vehicleID := t.VehicleID
		if p.VehicleID != nil {
			vehicleID = *p.VehicleID
		}
		seats := t.SeatsTotal
		if p.SeatsTotal != nil {
			seats = *p.SeatsTotal
		}
		if vehicleID != "" {
			v, err := ownedVehicle(ctx, tx, driverID, vehicleID)
			if errors.Is(err, ride.ErrNotFound) {
				return &ride.FieldError{Field: "vehicle_id", Reason: "not_found"}
			}
			if err != nil {
				return err
			}
			if v.PassengerSeats != nil && seats > *v.PassengerSeats {
				return &ride.FieldError{Field: "seats_total", Reason: "exceeds_vehicle"}
			}
		}
		if seats < ride.MaxLoad(t.Occupants, len(t.Stops)) {
			return ride.ErrSeatsBelowBooked
		}

		departs, arrives := t.DepartsAt, t.ArrivesAt
		if p.ScheduleChange() {
			var active int
			if err := tx.QueryRow(ctx, `
				SELECT COUNT(*) FROM bookings WHERE trip_id = $1::uuid AND status IN ('pending', 'approved')
			`, tripID).Scan(&active); err != nil {
				return dbErr("active bookings", err)
			}
			if active > 0 {
				return ride.ErrTripHasBookings
			}
			departs, arrives = *p.DepartsAt, *p.ArrivesAt
			if err := ride.ValidateSchedule(departs, arrives, now); err != nil {
				return err
			}
			if err := checkOverlap(ctx, tx, driverID, tripID, departs, arrives, buffer); err != nil {
				return err
			}
			if err := shiftStops(ctx, tx, t, departs, arrives); err != nil {
				return err
			}
		}

		var amen *ride.Amenities = p.Amenities
		if amen == nil {
			amen = &ride.Amenities{}
		}
		_, err = tx.Exec(ctx, `
			UPDATE trips SET
				vehicle_id = $2::uuid, seats_total = $3, departs_at = $4, arrives_at = $5,
				suggested_price = CASE WHEN $6 THEN NULL WHEN $7::float8 IS NULL THEN suggested_price ELSE $7 END,
				price_negotiable = COALESCE($8, price_negotiable),
				note = CASE WHEN $9::text IS NULL THEN note ELSE NULLIF($9, '') END,
				conditions = CASE WHEN $10::text IS NULL THEN conditions ELSE NULLIF($10, '') END,
				air_conditioning = COALESCE(NULLIF($11, ''), air_conditioning),
				audio_system = COALESCE(NULLIF($12, ''), audio_system),
				music_policy = COALESCE(NULLIF($13, ''), music_policy),
				smoking_policy = COALESCE(NULLIF($14, ''), smoking_policy),
				pets_policy = COALESCE(NULLIF($15, ''), pets_policy),
				luggage_size = COALESCE(NULLIF($16, ''), luggage_size),
				child_seat = COALESCE(NULLIF($17, ''), child_seat),
				conversation = COALESCE(NULLIF($18, ''), conversation)
			WHERE id = $1::uuid
		`, tripID, nullString(vehicleID), seats, departs, arrives, p.ClearPrice, p.Price, p.PriceNegotiable,
			p.Description, p.Conditions, amen.AirConditioning, amen.AudioSystem, amen.MusicPolicy,
			amen.SmokingPolicy, amen.PetsPolicy, amen.LuggageSize, amen.ChildSeat, amen.Conversation)
		if isExclusion(err) {
			return &ride.OverlapError{}
		}
		if err != nil {
			return dbErr("update trip", err)
		}
		for order, mp := range p.MeetingPoints {
			tag, err := tx.Exec(ctx, `
				UPDATE trip_stops SET meeting_point = NULLIF($3, '') WHERE trip_id = $1::uuid AND stop_order = $2
			`, tripID, order, mp)
			if err != nil {
				return dbErr("update meeting point", err)
			}
			if tag.RowsAffected() == 0 {
				return &ride.FieldError{Field: "meeting_points", Reason: "unknown_stop"}
			}
		}
		t.SeatsTotal = seats
		if err := refreshTripStatus(ctx, tx, t, t.Occupants); err != nil {
			return err
		}
		return notifyPassengers(ctx, tx, tripID, ride.NoteTripUpdated, driverID,
			fmt.Sprintf("trip_updated:%s:%d", tripID, now.UnixNano()), []ride.BookingStatus{ride.BookingApproved})
	})
}

// shiftStops moves every stop's planned time with the trip. An intermediate time that
// would fall outside the new schedule is cleared rather than guessed.
func shiftStops(ctx context.Context, tx pgx.Tx, t lockedTrip, departs, arrives time.Time) error {
	delta := departs.Sub(t.DepartsAt)
	last := len(t.Stops) - 1
	for _, s := range t.Stops {
		var planned *time.Time
		switch {
		case s.Order == 0:
			planned = &departs
		case s.Order == last:
			planned = &arrives
		case s.PlannedAt != nil:
			moved := s.PlannedAt.Add(delta)
			if moved.After(departs) && moved.Before(arrives) {
				planned = &moved
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE trip_stops SET planned_at = $3 WHERE trip_id = $1::uuid AND stop_order = $2
		`, t.ID, s.Order, optTime(planned)); err != nil {
			return dbErr("shift stop", err)
		}
	}
	return nil
}

// CancelTrip cancels a trip that has not departed and every pending or approved booking on
// it, in one transaction. A repeated cancel is a no-op.
func (db *AppDB) CancelTrip(ctx context.Context, driverID, tripID string, now time.Time) error {
	return db.inTx(ctx, "cancel trip", func(tx pgx.Tx) error {
		t, err := lockTrip(ctx, tx, tripID)
		if err != nil {
			return err
		}
		if t.DriverID != driverID {
			return ride.ErrForbidden
		}
		return cancelLockedTrip(ctx, tx, t, now)
	})
}

// cancelLockedTrip cancels t and its open bookings for its driver. The caller holds the
// trip lock.
func cancelLockedTrip(ctx context.Context, tx pgx.Tx, t lockedTrip, now time.Time) error {
	if t.Status == ride.TripCancelled {
		return nil
	}
	if t.Status == ride.TripInProgress || (t.Status.Bookable() && !t.DepartsAt.After(now)) {
		return ride.ErrTripStarted
	}
	if !ride.CanTransitionTrip(t.Status, ride.TripCancelled) {
		return ride.ErrTripNotActive
	}
	if _, err := tx.Exec(ctx, `
		UPDATE trips SET status = 'cancelled', cancelled_at = $2 WHERE id = $1::uuid
	`, t.ID, now); err != nil {
		return dbErr("cancel trip", err)
	}
	// Notify before the bookings change status, so the passenger set is still visible.
	if err := notifyPassengers(ctx, tx, t.ID, ride.NoteTripCancelled, t.DriverID,
		"trip_cancelled:"+t.ID, []ride.BookingStatus{ride.BookingPending, ride.BookingApproved}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE bookings SET status = 'cancelled_by_driver', cancelled_at = $2
		WHERE trip_id = $1::uuid AND status IN ('pending', 'approved')
	`, t.ID, now); err != nil {
		return dbErr("cancel trip bookings", err)
	}
	return nil
}

// CompleteTrip lets the driver close a trip after departure. A repeated call is a no-op.
func (db *AppDB) CompleteTrip(ctx context.Context, driverID, tripID string, now time.Time) error {
	return db.inTx(ctx, "complete trip", func(tx pgx.Tx) error {
		t, err := lockTrip(ctx, tx, tripID)
		if err != nil {
			return err
		}
		if t.DriverID != driverID {
			return ride.ErrForbidden
		}
		if t.Status == ride.TripCompleted {
			return nil
		}
		if !t.Status.Live() {
			return ride.ErrTripNotActive
		}
		if t.DepartsAt.After(now) {
			return ride.ErrTripNotStarted
		}
		return finishTrip(ctx, tx, tripID, ride.TripCompleted, now)
	})
}

// finishTrip moves a departed trip to completed or expired. Approved bookings complete and
// pending ones expire. The caller holds the trip lock.
func finishTrip(ctx context.Context, tx pgx.Tx, tripID string, status ride.TripStatus, now time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE trips SET status = $2::trip_status, completed_at = $3 WHERE id = $1::uuid
	`, tripID, status, now); err != nil {
		return dbErr("finish trip", err)
	}
	if err := expirePending(ctx, tx, tripID, now); err != nil {
		return err
	}
	if err := notifyPassengers(ctx, tx, tripID, ride.NoteTripCompleted, "",
		"trip_completed:"+tripID, []ride.BookingStatus{ride.BookingApproved}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE bookings SET status = 'completed', completed_at = $2 WHERE trip_id = $1::uuid AND status = 'approved'
	`, tripID, now); err != nil {
		return dbErr("complete bookings", err)
	}
	var driverID string
	if err := tx.QueryRow(ctx, `SELECT driver_id::text FROM trips WHERE id = $1::uuid`, tripID).Scan(&driverID); err != nil {
		return dbErr("trip driver", err)
	}
	return insertEvents(ctx, tx, []ride.Event{{
		UserID: driverID, Type: ride.NoteTripCompleted, TripID: tripID, DedupeKey: "trip_completed:" + tripID,
	}})
}

func expirePending(ctx context.Context, tx pgx.Tx, tripID string, now time.Time) error {
	rows, err := tx.Query(ctx, `
		UPDATE bookings SET status = 'expired', decided_at = $2
		WHERE trip_id = $1::uuid AND status = 'pending'
		RETURNING id::text, passenger_id::text
	`, tripID, now)
	if err != nil {
		return dbErr("expire pending", err)
	}
	var events []ride.Event
	for rows.Next() {
		var id, passenger string
		if err := rows.Scan(&id, &passenger); err != nil {
			rows.Close()
			return dbErr("scan expired", err)
		}
		events = append(events, ride.Event{
			UserID: passenger, Type: ride.NoteBookingExpired, TripID: tripID, BookingID: id, DedupeKey: "booking_expired:" + id,
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return dbErr("expired rows", err)
	}
	return insertEvents(ctx, tx, events)
}

// DriverTrips lists the driver's trips. scope is "upcoming" or "history".
func (db *AppDB) DriverTrips(ctx context.Context, driverID, scope string, page, limit int) ([]ride.Trip, bool, error) {
	statuses, order := []string{"active", "full", "in_progress"}, "departs_at ASC"
	if scope == "history" {
		statuses, order = []string{"completed", "cancelled", "expired"}, "departs_at DESC"
	}
	rows, err := db.pool.Query(ctx, `
		SELECT id::text FROM trips
		WHERE driver_id = $1::uuid AND status::text = ANY($2)
		ORDER BY `+order+`, id LIMIT $3 OFFSET $4
	`, driverID, statuses, limit+1, (page-1)*limit)
	if err != nil {
		return nil, false, dbErr("driver trips", err)
	}
	ids, err := collectIDs(rows)
	if err != nil {
		return nil, false, err
	}
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	trips, err := loadTrips(ctx, db.pool, ids, driverID)
	return trips, more, err
}

func collectIDs(rows pgx.Rows) ([]string, error) {
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, dbErr("scan id", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SearchTrips finds bookable trips where a pickup stop in the "from" area comes before a
// drop-off stop in the "to" area, with at least q.Seats free on every segment between them.
// A welayat matches its active child locations recursively. Dates and times are local to tz.
func (db *AppDB) SearchTrips(ctx context.Context, q ride.SearchQuery, now time.Time, tz string) ([]ride.Trip, bool, error) {
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	var ctes []string
	areaCTE := func(name, id string) {
		p := arg(id)
		ctes = append(ctes, fmt.Sprintf(`%s AS (
			SELECT id FROM locations WHERE id = %s::uuid AND is_active
			UNION
			SELECT l.id FROM locations l JOIN %s a ON l.parent_id = a.id WHERE l.is_active
		)`, name, p, name))
	}
	where := []string{
		"t.status IN ('active', 'full')",
		"t.departs_at > " + arg(now),
		"NOT u.is_blocked",
	}
	if q.ViewerID != "" {
		v := arg(q.ViewerID)
		where = append(where, "t.driver_id <> "+v+"::uuid", fmt.Sprintf(`NOT EXISTS (
			SELECT 1 FROM user_blocks ub
			WHERE (ub.blocker_id = %[1]s::uuid AND ub.blocked_id = t.driver_id)
			   OR (ub.blocker_id = t.driver_id AND ub.blocked_id = %[1]s::uuid))`, v))
	}
	if q.FromID != "" {
		areaCTE("from_area", q.FromID)
		where = append(where, "ps.location_id IN (SELECT id FROM from_area)")
	} else {
		where = append(where, "ps.stop_order = 0")
	}
	if q.ToID != "" {
		areaCTE("to_area", q.ToID)
		where = append(where, "ds.location_id IN (SELECT id FROM to_area)")
	} else {
		where = append(where, "ds.stop_order = (SELECT MAX(x.stop_order) FROM trip_stops x WHERE x.trip_id = t.id)")
	}
	local := ""
	if q.Date != nil || q.TimeFrom != "" || q.TimeTo != "" {
		local = "(COALESCE(ps.planned_at, t.departs_at) AT TIME ZONE " + arg(tz) + "::text)"
	}
	if q.Date != nil {
		where = append(where, local+"::date = "+arg(q.Date.Format("2006-01-02"))+"::date")
	}
	if q.TimeFrom != "" {
		where = append(where, local+"::time >= "+arg(q.TimeFrom)+"::time")
	}
	if q.TimeTo != "" {
		where = append(where, local+"::time <= "+arg(q.TimeTo)+"::time")
	}
	if q.PriceMin != nil {
		where = append(where, "t.suggested_price >= "+arg(*q.PriceMin))
	}
	if q.PriceMax != nil {
		where = append(where, "t.suggested_price <= "+arg(*q.PriceMax))
	}
	if q.AirCond {
		where = append(where, "t.air_conditioning = 'yes'")
	}
	if q.PetsAllowed {
		where = append(where, "t.pets_policy = 'allowed'")
	}
	if q.NoSmoking {
		where = append(where, "t.smoking_policy = 'prohibited'")
	}
	if q.SmokingOK {
		where = append(where, "t.smoking_policy = 'allowed'")
	}
	if q.ChildSeat {
		where = append(where, "t.child_seat = 'yes'")
	}
	if q.MinLuggage != "" {
		var sizes []string
		for _, s := range []string{"small", "medium", "large"} {
			if ride.LuggageRank(s) >= ride.LuggageRank(q.MinLuggage) {
				sizes = append(sizes, s)
			}
		}
		where = append(where, "t.luggage_size = ANY("+arg(sizes)+")")
	}
	if q.MinRating != nil {
		where = append(where, "u.rating_count > 0 AND u.rating_avg >= "+arg(*q.MinRating))
	}
	order := "c.pickup_at, c.id"
	switch q.Sort {
	case "price_asc":
		order = "c.price ASC NULLS LAST, c.pickup_at, c.id"
	case "price_desc":
		order = "c.price DESC NULLS LAST, c.pickup_at, c.id"
	case "rating":
		order = "c.rating DESC, c.pickup_at, c.id"
	}
	seats := arg(q.Seats)
	limit := arg(q.Limit + 1)
	offset := arg((q.Page - 1) * q.Limit)

	with := "WITH RECURSIVE "
	if len(ctes) > 0 {
		with += strings.Join(ctes, ",\n") + ",\n"
	}
	sql := with + `cand AS (
		SELECT DISTINCT ON (t.id) t.id, ps.stop_order AS pickup, ds.stop_order AS dropoff,
		       COALESCE(ps.planned_at, t.departs_at) AS pickup_at, t.suggested_price AS price,
		       u.rating_avg AS rating, t.seats_total
		FROM trips t
		JOIN users u ON u.id = t.driver_id
		JOIN trip_stops ps ON ps.trip_id = t.id AND ps.stop_type IN ('pickup', 'both')
		JOIN trip_stops ds ON ds.trip_id = t.id AND ds.stop_order > ps.stop_order AND ds.stop_type IN ('dropoff', 'both')
		WHERE ` + strings.Join(where, "\n AND ") + `
		ORDER BY t.id, ps.stop_order, ds.stop_order DESC
	)
	SELECT c.id::text, c.pickup, c.dropoff, (c.seats_total - occ.max_load)::int
	FROM cand c
	CROSS JOIN LATERAL (
		SELECT COALESCE(MAX(seg.load), 0) AS max_load FROM (
			SELECT g.s, COALESCE(SUM(b.seats), 0) AS load
			FROM generate_series(c.pickup, c.dropoff - 1) AS g(s)
			LEFT JOIN bookings b ON b.trip_id = c.id AND b.status = 'approved'
			     AND b.pickup_order <= g.s AND b.dropoff_order > g.s
			GROUP BY g.s
		) seg
	) occ
	WHERE c.seats_total - occ.max_load >= ` + seats + `
	ORDER BY ` + order + `
	LIMIT ` + limit + ` OFFSET ` + offset

	rows, err := db.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, false, dbErr("search trips", err)
	}
	var ids []string
	matches := map[string]ride.SegmentMatch{}
	for rows.Next() {
		var id string
		var m ride.SegmentMatch
		if err := rows.Scan(&id, &m.PickupOrder, &m.DropoffOrder, &m.FreeSeats); err != nil {
			rows.Close()
			return nil, false, dbErr("scan search", err)
		}
		ids = append(ids, id)
		matches[id] = m
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, dbErr("search rows", err)
	}
	more := len(ids) > q.Limit
	if more {
		ids = ids[:q.Limit]
	}
	trips, err := loadTrips(ctx, db.pool, ids, q.ViewerID)
	if err != nil {
		return nil, false, err
	}
	for i := range trips {
		m := matches[trips[i].ID]
		trips[i].Match = &m
	}
	return trips, more, nil
}
