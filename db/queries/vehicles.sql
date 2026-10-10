-- name: ListVehiclesByUser :many
SELECT id, user_id, brand, model, color, plate, created_at
FROM vehicles
WHERE user_id = $1
ORDER BY created_at ASC, id ASC;

-- name: CountVehiclesByUser :one
SELECT count(*)::int AS vehicle_count
FROM vehicles
WHERE user_id = $1;

-- name: CreateVehicle :one
INSERT INTO vehicles (user_id, brand, model, color, plate)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, user_id, brand, model, color, plate, created_at;

-- name: GetVehicleByID :one
SELECT id, user_id, brand, model, color, plate, created_at
FROM vehicles
WHERE id = $1;

-- name: UpdateVehicle :one
UPDATE vehicles
SET brand = $3, model = $4, color = $5, plate = $6
WHERE id = $1 AND user_id = $2
RETURNING id, user_id, brand, model, color, plate, created_at;

-- name: DeleteVehicle :execrows
DELETE FROM vehicles
WHERE id = $1 AND user_id = $2;

-- name: DeleteVehiclesByUser :exec
DELETE FROM vehicles WHERE user_id = $1;

-- name: GetLatestVehicleByUser :one
SELECT id, user_id, brand, model, color, plate, created_at
FROM vehicles
WHERE user_id = $1
ORDER BY created_at DESC, id DESC
LIMIT 1;
