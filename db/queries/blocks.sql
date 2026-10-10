-- name: BlockExistsEitherWay :one
SELECT EXISTS (
    SELECT 1
    FROM user_blocks
    WHERE (blocker_id = $1 AND blocked_id = $2)
       OR (blocker_id = $2 AND blocked_id = $1)
) AS blocked;

-- name: InsertUserBlock :execrows
INSERT INTO user_blocks (blocker_id, blocked_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: GetUserBlock :one
SELECT blocker_id, blocked_id, created_at
FROM user_blocks
WHERE blocker_id = $1 AND blocked_id = $2;

-- name: DeleteUserBlock :execrows
DELETE FROM user_blocks
WHERE blocker_id = $1 AND blocked_id = $2;

-- name: ListBlocksByBlocker :many
SELECT
    ub.blocked_id,
    ub.created_at,
    u.name,
    u.photo_url
FROM user_blocks ub
JOIN users u ON u.id = ub.blocked_id
WHERE ub.blocker_id = sqlc.arg(blocker_id)
ORDER BY ub.created_at DESC, ub.blocked_id DESC
LIMIT sqlc.arg(page_limit)
OFFSET sqlc.arg(page_offset);
