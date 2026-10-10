-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (user_id, token_hash, device_id, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING id;

-- name: GetRefreshTokenByHashForUpdate :one
SELECT *
FROM refresh_tokens
WHERE token_hash = $1
FOR UPDATE;

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens
SET revoked_at = now()
WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeDeviceRefreshTokens :execrows
UPDATE refresh_tokens
SET revoked_at = now()
WHERE user_id = $1 AND device_id = $2 AND revoked_at IS NULL;

-- name: RevokeAllUserRefreshTokens :execrows
UPDATE refresh_tokens
SET revoked_at = now()
WHERE user_id = $1 AND revoked_at IS NULL;

-- name: DeleteExpiredRefreshTokens :execrows
DELETE FROM refresh_tokens
WHERE expires_at < now()
   OR revoked_at < sqlc.arg(revoked_before);
