-- Account deletion and report handling.
--
-- A deleted account stays as an empty row, because trips, bookings and reviews of other
-- users reference it. Its phone number is released (NULL), so the number can register
-- again. The one exception is an account that an admin blocked: it keeps its number, so
-- deleting the account does not lift the ban.
ALTER TABLE users
    ALTER COLUMN phone DROP NOT NULL,
    ADD COLUMN deleted_at timestamptz;

-- Reports are opened by users and closed by an admin.
ALTER TABLE reports
    ADD COLUMN resolved_at timestamptz,
    ADD CONSTRAINT reports_status_known CHECK (status IN ('open', 'resolved')),
    ADD CONSTRAINT reports_reason_known CHECK (reason IN (
        'spam', 'fraud', 'abuse', 'no_show', 'unsafe', 'fake_profile', 'inappropriate_content', 'other'
    )),
    ADD CONSTRAINT reports_details_length CHECK (details IS NULL OR char_length(details) <= 1000);

CREATE INDEX reports_reporter_id_idx ON reports (reporter_id);
