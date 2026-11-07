-- Firebase Cloud Messaging device tokens. A token identifies one app install, so it
-- belongs to whoever signed in on that device last.
CREATE TABLE push_tokens (
    token text PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    platform text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT push_tokens_platform_known CHECK (platform IN ('android', 'ios')),
    CONSTRAINT push_tokens_token_length CHECK (char_length(token) BETWEEN 1 AND 4096)
);

CREATE INDEX push_tokens_user_id_idx ON push_tokens (user_id);
CREATE INDEX push_tokens_updated_at_idx ON push_tokens (updated_at);

-- Push delivery is an outbox over the inbox: a row is claimed once by setting pushed_at.
ALTER TABLE notifications ADD COLUMN pushed_at timestamptz;

-- Rows written before pushes existed are never sent.
UPDATE notifications SET pushed_at = created_at;

CREATE INDEX notifications_unpushed_idx ON notifications (created_at) WHERE pushed_at IS NULL;
