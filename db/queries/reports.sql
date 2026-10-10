-- name: CreateReport :one
INSERT INTO reports (reporter_id, reported_user_id, trip_id, reason, details)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListReports :many
SELECT *
FROM reports
WHERE sqlc.narg(status_filter)::text IS NULL OR status = sqlc.narg(status_filter)::text
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)
OFFSET sqlc.arg(page_offset);

-- name: UpdateReportStatus :one
UPDATE reports
SET status = $2
WHERE id = $1
RETURNING *;
