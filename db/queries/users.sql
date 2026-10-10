-- name: GetUserByPhone :one
SELECT * FROM users WHERE phone = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: CreateUserIfMissing :execrows
INSERT INTO users (phone, locale)
VALUES ($1, $2)
ON CONFLICT (phone) DO NOTHING;
