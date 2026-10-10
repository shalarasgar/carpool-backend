-- name: BlockExistsEitherWay :one
SELECT EXISTS (
    SELECT 1
    FROM user_blocks
    WHERE (blocker_id = $1 AND blocked_id = $2)
       OR (blocker_id = $2 AND blocked_id = $1)
) AS blocked;
