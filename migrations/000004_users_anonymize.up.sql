-- Soft-delete support for DELETE /v1/me.
-- The cleartext phone is replaced with a unique tombstone so the E.164 value
-- can be used by a new account. See README (Phase C assumptions).

ALTER TABLE users
    ADD COLUMN deleted_at timestamptz;

ALTER TABLE users DROP CONSTRAINT users_phone_e164;

ALTER TABLE users ADD CONSTRAINT users_phone_format CHECK (
    (deleted_at IS NULL AND phone ~ '^\+993[0-9]{8}$')
    OR (
        deleted_at IS NOT NULL
        AND phone ~ '^anon:[0-9a-f]{64}:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
    )
);

CREATE INDEX users_deleted_at_idx ON users (deleted_at) WHERE deleted_at IS NOT NULL;
