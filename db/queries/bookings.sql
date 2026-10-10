-- name: CreateBooking :one
INSERT INTO bookings (trip_id, passenger_id, seats)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetBookingForUpdate :one
SELECT * FROM bookings WHERE id = $1 FOR UPDATE;

-- name: GetBookingView :one
SELECT
    b.id,
    b.trip_id,
    b.passenger_id,
    b.seats,
    b.status,
    b.created_at,
    b.updated_at,
    u.name AS passenger_name,
    u.photo_url AS passenger_photo_url,
    t.driver_id
FROM bookings b
JOIN users u ON u.id = b.passenger_id
JOIN trips t ON t.id = b.trip_id
WHERE b.id = $1;

-- name: UpdateBookingStatus :one
UPDATE bookings
SET status = $2
WHERE id = $1
RETURNING *;

-- name: SumApprovedSeats :one
SELECT COALESCE(SUM(seats), 0)::int AS seats
FROM bookings
WHERE trip_id = $1 AND status = 'approved';

-- name: ListLiveBookingsByPassenger :many
SELECT *
FROM bookings
WHERE passenger_id = $1
  AND status IN ('pending', 'approved')
ORDER BY trip_id, id;

-- name: CancelLiveBookingsForTrip :many
UPDATE bookings
SET status = 'cancelled_by_driver'
WHERE trip_id = $1
  AND status IN ('pending', 'approved')
RETURNING *;

-- name: ListBookingsByTrip :many
SELECT
    b.id,
    b.trip_id,
    b.passenger_id,
    b.seats,
    b.status,
    b.created_at,
    b.updated_at,
    u.name AS passenger_name,
    u.photo_url AS passenger_photo_url,
    t.driver_id
FROM bookings b
JOIN users u ON u.id = b.passenger_id
JOIN trips t ON t.id = b.trip_id
WHERE b.trip_id = sqlc.arg(trip_id)
ORDER BY b.created_at ASC, b.id ASC
LIMIT sqlc.arg(page_limit)::int
OFFSET sqlc.arg(page_offset)::int;

-- name: ListBookingsByPassenger :many
SELECT
    b.id,
    b.trip_id,
    b.passenger_id,
    b.seats,
    b.status,
    b.created_at,
    b.updated_at,
    u.name AS passenger_name,
    u.photo_url AS passenger_photo_url,
    t.driver_id,
    t.vehicle_id,
    t.from_location_id,
    t.to_location_id,
    t.departs_at,
    t.seats_total,
    t.seats_left,
    t.suggested_price,
    t.currency,
    t.pickup_note,
    t.dropoff_note,
    t.note,
    t.status AS trip_status,
    t.created_at AS trip_created_at,
    t.updated_at AS trip_updated_at,
    d.name AS driver_name,
    d.photo_url AS driver_photo_url,
    d.rating_avg AS driver_rating_avg,
    d.rating_count AS driver_rating_count,
    v.brand AS vehicle_brand,
    v.model AS vehicle_model,
    v.color AS vehicle_color,
    v.plate AS vehicle_plate,
    fl.type AS from_type,
    fl.name_tk AS from_name_tk,
    fl.name_ru AS from_name_ru,
    fl.name_en AS from_name_en,
    tl.type AS to_type,
    tl.name_tk AS to_name_tk,
    tl.name_ru AS to_name_ru,
    tl.name_en AS to_name_en
FROM bookings b
JOIN users u ON u.id = b.passenger_id
JOIN trips t ON t.id = b.trip_id
JOIN users d ON d.id = t.driver_id
LEFT JOIN vehicles v ON v.id = t.vehicle_id
JOIN locations fl ON fl.id = t.from_location_id
JOIN locations tl ON tl.id = t.to_location_id
WHERE b.passenger_id = sqlc.arg(passenger_id)
ORDER BY b.created_at DESC, b.id DESC
LIMIT sqlc.arg(page_limit)::int
OFFSET sqlc.arg(page_offset)::int;

-- name: GetApprovedBookingForPassenger :one
SELECT *
FROM bookings
WHERE trip_id = $1
  AND passenger_id = $2
  AND status = 'approved'
FOR UPDATE;

-- Pending bookings whose trip has already departed become cancelled_by_driver.
-- The driver never decided, so the row is not rejected. The passenger is
-- notified in the same statement. Approved bookings are left as they are.
-- name: CancelStalePendingBookings :execrows
WITH cancelled AS (
    UPDATE bookings AS b
    SET status = 'cancelled_by_driver'
    FROM trips AS t
    WHERE b.trip_id = t.id
      AND b.status = 'pending'
      AND t.departs_at <= now()
    RETURNING b.id, b.trip_id, b.passenger_id, t.driver_id
)
INSERT INTO notifications (user_id, type, payload)
SELECT
    c.passenger_id,
    'booking_cancelled',
    jsonb_build_object(
        'actor_id', c.driver_id,
        'trip_id', c.trip_id,
        'booking_id', c.id
    )
FROM cancelled AS c;
