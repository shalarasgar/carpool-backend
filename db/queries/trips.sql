-- name: CreateTrip :one
INSERT INTO trips (
    driver_id,
    vehicle_id,
    from_location_id,
    to_location_id,
    departs_at,
    seats_total,
    seats_left,
    suggested_price,
    currency,
    pickup_note,
    dropoff_note,
    note
) VALUES (
    $1, $2, $3, $4, $5, $6, $6, $7, $8, $9, $10, $11
)
RETURNING *;

-- name: GetTripForUpdate :one
SELECT * FROM trips WHERE id = $1 FOR UPDATE;

-- name: SaveTrip :one
UPDATE trips
SET
    vehicle_id = $2,
    from_location_id = $3,
    to_location_id = $4,
    departs_at = $5,
    seats_total = $6,
    seats_left = $7,
    suggested_price = $8,
    currency = $9,
    pickup_note = $10,
    dropoff_note = $11,
    note = $12,
    status = $13
WHERE id = $1
RETURNING *;

-- name: UpdateTripSeats :one
UPDATE trips
SET seats_left = $2, status = $3
WHERE id = $1
RETURNING *;

-- name: SetTripStatus :execrows
UPDATE trips SET status = $2 WHERE id = $1;

-- name: ListLiveTripIDsByDriver :many
SELECT id
FROM trips
WHERE driver_id = $1 AND status IN ('active', 'full')
ORDER BY id;

-- name: GetTripView :one
SELECT
    t.id,
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
    t.status,
    t.created_at,
    t.updated_at,
    u.name AS driver_name,
    u.photo_url AS driver_photo_url,
    u.rating_avg AS driver_rating_avg,
    u.rating_count AS driver_rating_count,
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
FROM trips t
JOIN users u ON u.id = t.driver_id
LEFT JOIN vehicles v ON v.id = t.vehicle_id
JOIN locations fl ON fl.id = t.from_location_id
JOIN locations tl ON tl.id = t.to_location_id
WHERE t.id = $1;

-- name: ListDriverTrips :many
SELECT
    t.id,
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
    t.status,
    t.created_at,
    t.updated_at,
    u.name AS driver_name,
    u.photo_url AS driver_photo_url,
    u.rating_avg AS driver_rating_avg,
    u.rating_count AS driver_rating_count,
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
FROM trips t
JOIN users u ON u.id = t.driver_id
LEFT JOIN vehicles v ON v.id = t.vehicle_id
JOIN locations fl ON fl.id = t.from_location_id
JOIN locations tl ON tl.id = t.to_location_id
WHERE t.driver_id = sqlc.arg(driver_id)
  AND (
      sqlc.narg(status)::text IS NULL
      OR t.status = sqlc.narg(status)::trip_status
  )
ORDER BY t.departs_at DESC, t.id DESC
LIMIT sqlc.arg(page_limit)::int
OFFSET sqlc.arg(page_offset)::int;

-- name: SearchTrips :many
-- from_tree / to_tree walk every descendant, including through inactive
-- parents, and the filter keeps only active locations. Depth is capped so a
-- bad parent cycle cannot loop.
WITH RECURSIVE from_tree AS (
    SELECT id, is_active, 1 AS depth
    FROM locations
    WHERE id = sqlc.narg(from_id)::uuid
    UNION ALL
    SELECT c.id, c.is_active, p.depth + 1
    FROM locations c
    JOIN from_tree p ON c.parent_id = p.id
    WHERE p.depth < 8
),
to_tree AS (
    SELECT id, is_active, 1 AS depth
    FROM locations
    WHERE id = sqlc.narg(to_id)::uuid
    UNION ALL
    SELECT c.id, c.is_active, p.depth + 1
    FROM locations c
    JOIN to_tree p ON c.parent_id = p.id
    WHERE p.depth < 8
)
SELECT
    t.id,
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
    t.status,
    t.created_at,
    t.updated_at,
    u.name AS driver_name,
    u.photo_url AS driver_photo_url,
    u.rating_avg AS driver_rating_avg,
    u.rating_count AS driver_rating_count,
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
FROM trips t
JOIN users u ON u.id = t.driver_id
LEFT JOIN vehicles v ON v.id = t.vehicle_id
JOIN locations fl ON fl.id = t.from_location_id
JOIN locations tl ON tl.id = t.to_location_id
WHERE t.status = 'active'
  AND t.departs_at > now()
  AND t.seats_left >= sqlc.arg(min_seats)
  AND u.deleted_at IS NULL
  AND NOT u.is_blocked
  AND NOT EXISTS (
      SELECT 1
      FROM user_blocks ub
      WHERE (ub.blocker_id = sqlc.arg(viewer_id) AND ub.blocked_id = t.driver_id)
         OR (ub.blocker_id = t.driver_id AND ub.blocked_id = sqlc.arg(viewer_id))
  )
  AND (
      sqlc.narg(from_id)::uuid IS NULL
      OR t.from_location_id IN (SELECT id FROM from_tree WHERE is_active)
  )
  AND (
      sqlc.narg(to_id)::uuid IS NULL
      OR t.to_location_id IN (SELECT id FROM to_tree WHERE is_active)
  )
  AND (
      sqlc.narg(depart_from)::timestamptz IS NULL
      OR t.departs_at >= sqlc.narg(depart_from)
  )
  AND (
      sqlc.narg(depart_until)::timestamptz IS NULL
      OR t.departs_at < sqlc.narg(depart_until)
  )
ORDER BY t.departs_at ASC, t.id ASC
LIMIT sqlc.arg(page_limit)::int
OFFSET sqlc.arg(page_offset)::int;

-- Past active/full trips with at least one approved booking become completed
-- only when seats_left equals seats_total minus those approved seats.
-- A mismatch is left untouched (see CountInconsistentPastTrips).
-- name: CompleteConsistentPastTrips :execrows
UPDATE trips AS t
SET status = 'completed'
WHERE t.status IN ('active', 'full')
  AND t.departs_at <= now()
  AND EXISTS (
      SELECT 1 FROM bookings b
      WHERE b.trip_id = t.id AND b.status = 'approved'
  )
  AND t.seats_left = t.seats_total - COALESCE((
      SELECT SUM(b.seats)::int
      FROM bookings b
      WHERE b.trip_id = t.id AND b.status = 'approved'
  ), 0);

-- Past active/full trips with no approved booking become expired.
-- Booking rows are not modified.
-- name: ExpirePastTripsWithoutApproval :execrows
UPDATE trips AS t
SET status = 'expired'
WHERE t.status IN ('active', 'full')
  AND t.departs_at <= now()
  AND NOT EXISTS (
      SELECT 1 FROM bookings b
      WHERE b.trip_id = t.id AND b.status = 'approved'
  );

-- name: CountInconsistentPastTrips :one
SELECT count(*)::int AS trips
FROM trips t
WHERE t.status IN ('active', 'full')
  AND t.departs_at <= now()
  AND EXISTS (
      SELECT 1 FROM bookings b
      WHERE b.trip_id = t.id AND b.status = 'approved'
  )
  AND t.seats_left <> t.seats_total - COALESCE((
      SELECT SUM(b.seats)::int
      FROM bookings b
      WHERE b.trip_id = t.id AND b.status = 'approved'
  ), 0);
