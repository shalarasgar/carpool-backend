-- name: ListLocations :many
SELECT id, parent_id, type, name_tk, name_ru, name_en, is_active, sort_order
FROM locations
ORDER BY sort_order ASC, name_en ASC;
