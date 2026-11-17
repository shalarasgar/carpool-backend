DROP INDEX IF EXISTS reports_reporter_id_idx;

ALTER TABLE reports
    DROP CONSTRAINT IF EXISTS reports_details_length,
    DROP CONSTRAINT IF EXISTS reports_reason_known,
    DROP CONSTRAINT IF EXISTS reports_status_known,
    DROP COLUMN IF EXISTS resolved_at;

-- Deleted accounts have no phone number and their rows cannot be removed while other
-- users' trips and bookings reference them. This step fails if any exist; that is
-- intended, because the old schema cannot represent them.
ALTER TABLE users
    DROP COLUMN IF EXISTS deleted_at,
    ALTER COLUMN phone SET NOT NULL;
