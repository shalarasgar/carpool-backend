-- Anonymized phones cannot satisfy the original E.164 check.
-- Drop those accounts and the rows that point at them, then restore the constraint.

DELETE FROM trip_stops
WHERE trip_id IN (
    SELECT id FROM trips
    WHERE driver_id IN (SELECT id FROM users WHERE deleted_at IS NOT NULL)
);

DELETE FROM bookings
WHERE passenger_id IN (SELECT id FROM users WHERE deleted_at IS NOT NULL)
   OR trip_id IN (
        SELECT id FROM trips
        WHERE driver_id IN (SELECT id FROM users WHERE deleted_at IS NOT NULL)
   );

DELETE FROM reviews
WHERE from_user_id IN (SELECT id FROM users WHERE deleted_at IS NOT NULL)
   OR to_user_id IN (SELECT id FROM users WHERE deleted_at IS NOT NULL)
   OR trip_id IN (
        SELECT id FROM trips
        WHERE driver_id IN (SELECT id FROM users WHERE deleted_at IS NOT NULL)
   );

DELETE FROM reports
WHERE reporter_id IN (SELECT id FROM users WHERE deleted_at IS NOT NULL)
   OR reported_user_id IN (SELECT id FROM users WHERE deleted_at IS NOT NULL)
   OR trip_id IN (
        SELECT id FROM trips
        WHERE driver_id IN (SELECT id FROM users WHERE deleted_at IS NOT NULL)
   );

DELETE FROM user_blocks
WHERE blocker_id IN (SELECT id FROM users WHERE deleted_at IS NOT NULL)
   OR blocked_id IN (SELECT id FROM users WHERE deleted_at IS NOT NULL);

DELETE FROM trips
WHERE driver_id IN (SELECT id FROM users WHERE deleted_at IS NOT NULL);

DELETE FROM vehicles
WHERE user_id IN (SELECT id FROM users WHERE deleted_at IS NOT NULL);

DELETE FROM refresh_tokens
WHERE user_id IN (SELECT id FROM users WHERE deleted_at IS NOT NULL);

DELETE FROM device_tokens
WHERE user_id IN (SELECT id FROM users WHERE deleted_at IS NOT NULL);

DELETE FROM users WHERE deleted_at IS NOT NULL;

DROP INDEX IF EXISTS users_deleted_at_idx;

ALTER TABLE users DROP CONSTRAINT users_phone_format;
ALTER TABLE users ADD CONSTRAINT users_phone_e164 CHECK (phone ~ '^\+993[0-9]{8}$');
ALTER TABLE users DROP COLUMN deleted_at;
