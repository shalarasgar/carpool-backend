-- name: InvalidateActiveOTPs :exec
UPDATE otp_codes
SET used = true
WHERE phone = $1 AND used = false;

-- name: CreateOTP :one
INSERT INTO otp_codes (phone, code_hash, expires_at, ip, device_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING id;

-- name: GetLatestOTPForUpdate :one
SELECT *
FROM otp_codes
WHERE phone = $1 AND used = false
ORDER BY created_at DESC
LIMIT 1
FOR UPDATE;

-- name: IncrementOTPAttempts :exec
UPDATE otp_codes
SET attempts = attempts + 1,
    used = (attempts + 1 >= sqlc.arg(max_attempts)::int)
WHERE id = $1;

-- name: MarkOTPUsed :exec
UPDATE otp_codes SET used = true WHERE id = $1;

-- name: DeleteExpiredOTPs :execrows
DELETE FROM otp_codes
WHERE expires_at < sqlc.arg(before);
