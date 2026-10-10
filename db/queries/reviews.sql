-- name: CreateReview :one
INSERT INTO reviews (trip_id, from_user_id, to_user_id, rating, comment)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListReviewsForUser :many
SELECT
    r.id,
    r.trip_id,
    r.from_user_id,
    r.to_user_id,
    r.rating,
    r.comment,
    r.created_at,
    u.name AS from_name,
    u.photo_url AS from_photo_url
FROM reviews r
JOIN users u ON u.id = r.from_user_id
WHERE r.to_user_id = sqlc.arg(to_user_id)
ORDER BY r.created_at DESC, r.id DESC
LIMIT sqlc.arg(page_limit)
OFFSET sqlc.arg(page_offset);
