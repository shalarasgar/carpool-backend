package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shalarasgar/carpool-backend/internal/store"
)

// Vehicle is a car belonging to a user. UserID is not part of the value
// returned to handlers; ownership is checked before this value is built.
type Vehicle struct {
	ID        string
	Brand     string
	Model     string
	Color     string
	Plate     string
	CreatedAt time.Time
}

// VehicleInput is a create or a full replacement of the four text fields.
type VehicleInput struct {
	Brand string
	Model string
	Color string
	Plate string
}

// VehiclePatch is a partial update. Nil fields stay as they are.
type VehiclePatch struct {
	Brand *string
	Model *string
	Color *string
	Plate *string
}

// Vehicles implements the caller's garage.
type Vehicles struct {
	pool   *pgxpool.Pool
	q      *store.Queries
	max    int
	logger *slog.Logger
}

// NewVehicles wires the vehicle service. max is the per-user cap.
func NewVehicles(pool *pgxpool.Pool, max int, logger *slog.Logger) *Vehicles {
	if logger == nil {
		logger = slog.Default()
	}
	return &Vehicles{pool: pool, q: store.New(pool), max: max, logger: logger}
}

// List returns the caller's vehicles, oldest first.
func (v *Vehicles) List(ctx context.Context, callerID string) ([]Vehicle, error) {
	uid, err := parseUserID(callerID)
	if err != nil {
		return nil, err
	}
	rows, err := v.q.ListVehiclesByUser(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("list vehicles: %w", err)
	}
	out := make([]Vehicle, 0, len(rows))
	for _, row := range rows {
		out = append(out, vehicleFromStore(row))
	}
	return out, nil
}

// Create adds a vehicle. The user row is locked so two parallel creates
// cannot pass the per-user cap.
func (v *Vehicles) Create(ctx context.Context, callerID string, in VehicleInput) (Vehicle, error) {
	fields, err := NormalizeVehicle(in.Brand, in.Model, in.Color, in.Plate)
	if err != nil {
		return Vehicle{}, err
	}
	uid, err := parseUserID(callerID)
	if err != nil {
		return Vehicle{}, err
	}
	var created store.Vehicle
	err = store.RunInTx(ctx, v.pool, func(q *store.Queries) error {
		user, err := q.GetUserByIDForUpdate(ctx, uid)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnauthorized
		}
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if user.DeletedAt.Valid {
			return ErrUnauthorized
		}
		if user.IsBlocked {
			return ErrUserBlocked
		}
		n, err := q.CountVehiclesByUser(ctx, uid)
		if err != nil {
			return fmt.Errorf("count vehicles: %w", err)
		}
		if err := vehicleLimit(int(n), v.max); err != nil {
			return err
		}
		created, err = q.CreateVehicle(ctx, store.CreateVehicleParams{
			UserID: uid,
			Brand:  fields.Brand,
			Model:  fields.Model,
			Color:  fields.Color,
			Plate:  fields.Plate,
		})
		if err != nil {
			return fmt.Errorf("create vehicle: %w", err)
		}
		return nil
	})
	if err != nil {
		return Vehicle{}, err
	}
	v.logger.Info("vehicle created", "user_id", callerID, "vehicle_id", created.ID.String())
	return vehicleFromStore(created), nil
}

// Update changes the caller's vehicle. A vehicle owned by someone else is
// ErrNotFound.
func (v *Vehicles) Update(ctx context.Context, callerID, vehicleID string, patch VehiclePatch) (Vehicle, error) {
	if patch.Brand == nil && patch.Model == nil && patch.Color == nil && patch.Plate == nil {
		return Vehicle{}, validationError(map[string]string{"body": "empty"})
	}
	row, err := v.owned(ctx, callerID, vehicleID)
	if err != nil {
		return Vehicle{}, err
	}
	brand, model, color, plate := row.Brand, row.Model, row.Color, row.Plate
	if patch.Brand != nil {
		brand = *patch.Brand
	}
	if patch.Model != nil {
		model = *patch.Model
	}
	if patch.Color != nil {
		color = *patch.Color
	}
	if patch.Plate != nil {
		plate = *patch.Plate
	}
	fields, err := NormalizeVehicle(brand, model, color, plate)
	if err != nil {
		return Vehicle{}, err
	}
	updated, err := v.q.UpdateVehicle(ctx, store.UpdateVehicleParams{
		ID:     row.ID,
		UserID: row.UserID,
		Brand:  fields.Brand,
		Model:  fields.Model,
		Color:  fields.Color,
		Plate:  fields.Plate,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Vehicle{}, ErrNotFound
	}
	if err != nil {
		return Vehicle{}, fmt.Errorf("update vehicle: %w", err)
	}
	v.logger.Info("vehicle updated", "user_id", callerID, "vehicle_id", vehicleID)
	return vehicleFromStore(updated), nil
}

// Delete removes the caller's vehicle. A vehicle owned by someone else is
// ErrNotFound.
func (v *Vehicles) Delete(ctx context.Context, callerID, vehicleID string) error {
	row, err := v.owned(ctx, callerID, vehicleID)
	if err != nil {
		return err
	}
	n, err := v.q.DeleteVehicle(ctx, store.DeleteVehicleParams{ID: row.ID, UserID: row.UserID})
	if err != nil {
		return fmt.Errorf("delete vehicle: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	v.logger.Info("vehicle deleted", "user_id", callerID, "vehicle_id", vehicleID)
	return nil
}

func (v *Vehicles) owned(ctx context.Context, callerID, vehicleID string) (store.Vehicle, error) {
	vid, err := parseUUID(vehicleID)
	if err != nil {
		return store.Vehicle{}, ErrNotFound
	}
	caller, err := parseUserID(callerID)
	if err != nil {
		return store.Vehicle{}, err
	}
	row, err := v.q.GetVehicleByID(ctx, vid)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Vehicle{}, ErrNotFound
	}
	if err != nil {
		return store.Vehicle{}, fmt.Errorf("load vehicle: %w", err)
	}
	if err := requireOwner(row.UserID.String(), caller.String()); err != nil {
		return store.Vehicle{}, err
	}
	return row, nil
}

func vehicleFromStore(v store.Vehicle) Vehicle {
	return Vehicle{
		ID:        v.ID.String(),
		Brand:     v.Brand,
		Model:     v.Model,
		Color:     v.Color,
		Plate:     v.Plate,
		CreatedAt: v.CreatedAt.Time,
	}
}
