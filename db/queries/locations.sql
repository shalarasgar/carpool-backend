-- name: GetLocationByID :one
SELECT id, parent_id, type, name_tk, name_ru, name_en, is_active, sort_order
FROM locations
WHERE id = $1;

-- name: SetLocationActive :one
UPDATE locations
SET is_active = $2
WHERE id = $1
RETURNING id, parent_id, type, name_tk, name_ru, name_en, is_active, sort_order;

-- name: ListLocations :many
SELECT id, parent_id, type, name_tk, name_ru, name_en, is_active, sort_order
FROM locations
ORDER BY sort_order ASC, name_en ASC;
