-- In-app notification inbox. Text is not stored: type and payload are
-- localized from the recipient's users.locale when the inbox is read.
-- payload is a JSON object and must not contain phone numbers.

CREATE TABLE notifications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id),
    type text NOT NULL,
    payload jsonb NOT NULL,
    read_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT notifications_type_not_empty CHECK (char_length(type) > 0),
    CONSTRAINT notifications_payload_object CHECK (jsonb_typeof(payload) = 'object')
);

CREATE INDEX notifications_user_created_idx
    ON notifications (user_id, created_at DESC, id DESC);
CREATE INDEX notifications_user_unread_idx
    ON notifications (user_id)
    WHERE read_at IS NULL;
CREATE INDEX notifications_read_at_idx
    ON notifications (read_at)
    WHERE read_at IS NOT NULL;

-- The spec leaves reason and status as text. These checks are the enums the
-- API accepts. Existing rows are empty in a fresh database; a row outside
-- the lists would stop this migration.

ALTER TABLE reports
    ADD CONSTRAINT reports_reason_known CHECK (
        reason IN ('spam', 'harassment', 'scam', 'unsafe_driving', 'no_show', 'other')
    ),
    ADD CONSTRAINT reports_status_known CHECK (
        status IN ('open', 'reviewing', 'resolved', 'dismissed')
    );
