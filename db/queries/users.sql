-- name: GetUserByPhone :one
SELECT * FROM users WHERE phone = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: CreateUserIfMissing :execrows
INSERT INTO users (phone, locale)
VALUES ($1, $2)
ON CONFLICT (phone) DO NOTHING;

-- name: GetUserByIDForUpdate :one
SELECT * FROM users WHERE id = $1 FOR UPDATE;

-- name: UpdateUserProfile :one
UPDATE users
SET
    name = CASE WHEN sqlc.arg(update_name)::boolean THEN sqlc.arg(name) ELSE name END,
    locale = CASE WHEN sqlc.arg(update_locale)::boolean THEN sqlc.arg(locale) ELSE locale END
WHERE id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- name: SetUserPhoto :one
UPDATE users
SET photo_url = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: AnonymizeUser :execrows
UPDATE users
SET name = '', photo_url = NULL, phone = $2, deleted_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: SetUserBlocked :execrows
UPDATE users
SET is_blocked = $2
WHERE id = $1 AND deleted_at IS NULL;

-- Recompute from this user's reviews so parallel ratings of the same person
-- stay exact. The caller inserts the new review first and already holds the
-- user row lock, so the two updates cannot interleave.
-- name: AddUserRating :one
UPDATE users AS u
SET
    rating_count = (
        SELECT count(*)::int FROM reviews AS r WHERE r.to_user_id = u.id
    ),
    rating_avg = (
        SELECT round(avg(r.rating)::numeric, 2) FROM reviews AS r WHERE r.to_user_id = u.id
    )
WHERE u.id = $1
RETURNING rating_avg, rating_count;

-- name: CountCompletedTripsAsDriver :one
SELECT count(*)::int AS completed_trips
FROM trips
WHERE driver_id = $1 AND status = 'completed';

-- name: GetActiveUserPhone :one
SELECT phone FROM users WHERE id = $1 AND deleted_at IS NULL;

-- name: CountCompletedTripsAsPassenger :one
SELECT count(*)::int AS completed_trips
FROM bookings b
JOIN trips t ON t.id = b.trip_id
WHERE b.passenger_id = $1
  AND b.status = 'approved'
  AND t.status = 'completed';
