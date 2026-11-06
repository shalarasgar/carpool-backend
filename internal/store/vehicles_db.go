package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/shalarasgar/carpool-backend/internal/ride"
)

const vehicleSelect = `
SELECT id::text, user_id::text, brand, model, color, plate, year::int, COALESCE(vehicle_type, ''),
       passenger_seats::int, COALESCE(photo_url, ''), created_at
FROM vehicles`

func scanVehicle(row pgx.Row) (ride.Vehicle, error) {
	var v ride.Vehicle
	err := row.Scan(&v.ID, &v.UserID, &v.Brand, &v.Model, &v.Color, &v.Plate, &v.Year, &v.Type,
		&v.PassengerSeats, &v.PhotoURL, &v.CreatedAt)
	return v, err
}

// ListVehicles returns the user's vehicles that are not deleted, oldest first.
func (db *AppDB) ListVehicles(ctx context.Context, userID string) ([]ride.Vehicle, error) {
	rows, err := db.pool.Query(ctx, vehicleSelect+`
		WHERE user_id = $1::uuid AND deleted_at IS NULL ORDER BY created_at
	`, userID)
	if err != nil {
		return nil, dbErr("list vehicles", err)
	}
	defer rows.Close()
	out := []ride.Vehicle{}
	for rows.Next() {
		v, err := scanVehicle(rows)
		if err != nil {
			return nil, dbErr("scan vehicle", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Vehicle loads one vehicle owned by userID.
func (db *AppDB) Vehicle(ctx context.Context, userID, id string) (ride.Vehicle, error) {
	return ownedVehicle(ctx, db.pool, userID, id)
}

func ownedVehicle(ctx context.Context, q dbtx, userID, id string) (ride.Vehicle, error) {
	v, err := scanVehicle(q.QueryRow(ctx, vehicleSelect+`
		WHERE id = $1::uuid AND user_id = $2::uuid AND deleted_at IS NULL
	`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
		return ride.Vehicle{}, ride.ErrNotFound
	}
	if err != nil {
		return ride.Vehicle{}, dbErr("load vehicle", err)
	}
	return v, nil
}

// CreateVehicle stores a validated vehicle.
func (db *AppDB) CreateVehicle(ctx context.Context, v ride.Vehicle) (ride.Vehicle, error) {
	var id string
	err := db.pool.QueryRow(ctx, `
		INSERT INTO vehicles (user_id, brand, model, color, plate, year, vehicle_type, passenger_seats)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, NULLIF($7, ''), $8)
		RETURNING id::text
	`, v.UserID, v.Brand, v.Model, v.Color, v.Plate, v.Year, v.Type, v.PassengerSeats).Scan(&id)
	if err != nil {
		return ride.Vehicle{}, dbErr("create vehicle", err)
	}
	return db.Vehicle(ctx, v.UserID, id)
}

// UpdateVehicle replaces the editable fields of an owned vehicle.
// Passenger seats cannot drop below the seats of a live trip that uses the vehicle.
func (db *AppDB) UpdateVehicle(ctx context.Context, v ride.Vehicle) (ride.Vehicle, error) {
	err := db.inTx(ctx, "update vehicle", func(tx pgx.Tx) error {
		if _, err := ownedVehicle(ctx, tx, v.UserID, v.ID); err != nil {
			return err
		}
		if v.PassengerSeats != nil {
			var maxSeats int
			if err := tx.QueryRow(ctx, `
				SELECT COALESCE(MAX(seats_total), 0) FROM trips
				WHERE vehicle_id = $1::uuid AND status IN ('active', 'full', 'in_progress')
			`, v.ID).Scan(&maxSeats); err != nil {
				return dbErr("vehicle trips", err)
			}
			if *v.PassengerSeats < maxSeats {
				return ride.ErrSeatsBelowBooked
			}
		}
		_, err := tx.Exec(ctx, `
			UPDATE vehicles SET brand = $3, model = $4, color = $5, plate = $6, year = $7,
				vehicle_type = NULLIF($8, ''), passenger_seats = $9
			WHERE id = $1::uuid AND user_id = $2::uuid
		`, v.ID, v.UserID, v.Brand, v.Model, v.Color, v.Plate, v.Year, v.Type, v.PassengerSeats)
		if err != nil {
			return dbErr("update vehicle", err)
		}
		return nil
	})
	if err != nil {
		return ride.Vehicle{}, err
	}
	return db.Vehicle(ctx, v.UserID, v.ID)
}

// DeleteVehicle soft-deletes a vehicle. A vehicle on a live trip cannot be deleted.
func (db *AppDB) DeleteVehicle(ctx context.Context, userID, id string) error {
	return db.inTx(ctx, "delete vehicle", func(tx pgx.Tx) error {
		if _, err := ownedVehicle(ctx, tx, userID, id); err != nil {
			return err
		}
		var live int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM trips WHERE vehicle_id = $1::uuid AND status IN ('active', 'full', 'in_progress')
		`, id).Scan(&live); err != nil {
			return dbErr("vehicle live trips", err)
		}
		if live > 0 {
			return ride.ErrTripHasBookings
		}
		if _, err := tx.Exec(ctx, `UPDATE vehicles SET deleted_at = now() WHERE id = $1::uuid`, id); err != nil {
			return dbErr("delete vehicle", err)
		}
		return nil
	})
}

// SetVehiclePhoto stores the photo URL of an owned vehicle and returns the previous one.
func (db *AppDB) SetVehiclePhoto(ctx context.Context, userID, id, url string) (string, error) {
	var old string
	err := db.pool.QueryRow(ctx, `
		UPDATE vehicles v SET photo_url = NULLIF($3, '')
		FROM (SELECT photo_url FROM vehicles WHERE id = $1::uuid) prev
		WHERE v.id = $1::uuid AND v.user_id = $2::uuid AND v.deleted_at IS NULL
		RETURNING COALESCE(prev.photo_url, '')
	`, id, userID, url).Scan(&old)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
		return "", ride.ErrNotFound
	}
	if err != nil {
		return "", dbErr("set vehicle photo", err)
	}
	return old, nil
}

// Locations returns active locations in display order. A location is listed only when
// its parent chain is active too.
func (db *AppDB) Locations(ctx context.Context) ([]ride.Location, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT l.id::text, COALESCE(l.parent_id::text, ''), l.type::text, l.name_tk, l.name_ru, l.name_en, l.sort_order
		FROM locations l
		LEFT JOIN locations p ON p.id = l.parent_id
		WHERE l.is_active AND (p.id IS NULL OR p.is_active)
		ORDER BY l.sort_order, l.name_tk
	`)
	if err != nil {
		return nil, dbErr("list locations", err)
	}
	defer rows.Close()
	out := []ride.Location{}
	for rows.Next() {
		var l ride.Location
		if err := rows.Scan(&l.ID, &l.ParentID, &l.Type, &l.NameTK, &l.NameRU, &l.NameEN, &l.SortOrder); err != nil {
			return nil, dbErr("scan location", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
