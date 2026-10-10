-- Enum values cannot be dropped in place. Rewrite any expired trips, then
-- recreate trip_status without that value.

UPDATE trips SET status = 'cancelled' WHERE status = 'expired';

ALTER TABLE trips ALTER COLUMN status DROP DEFAULT;
ALTER TABLE trips ALTER COLUMN status TYPE text;
DROP TYPE trip_status;
CREATE TYPE trip_status AS ENUM ('active', 'full', 'cancelled', 'completed');
ALTER TABLE trips
    ALTER COLUMN status TYPE trip_status USING status::trip_status,
    ALTER COLUMN status SET DEFAULT 'active';
