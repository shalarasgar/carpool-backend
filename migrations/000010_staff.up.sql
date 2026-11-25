-- Personal accounts for the admin panel, their sessions, and a log of what they did.
-- The static ADMIN_TOKEN still works for scripts and emergencies; its actions are logged
-- with a NULL staff_id and the name 'ADMIN_TOKEN'.
CREATE TABLE staff (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    login         text NOT NULL,
    name          text NOT NULL,
    role          text NOT NULL,
    password_hash text NOT NULL,
    created_by    uuid REFERENCES staff (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    disabled_at   timestamptz,
    CONSTRAINT staff_role_known CHECK (role IN ('admin', 'moderator')),
    CONSTRAINT staff_login_format CHECK (login ~ '^[a-z0-9][a-z0-9._-]{2,31}$'),
    CONSTRAINT staff_name_length CHECK (char_length(name) BETWEEN 1 AND 80)
);

CREATE UNIQUE INDEX staff_login_key ON staff (login);

-- Opaque bearer tokens; only their SHA-256 is stored.
CREATE TABLE staff_sessions (
    token_hash text PRIMARY KEY,
    staff_id   uuid NOT NULL REFERENCES staff (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);

CREATE INDEX staff_sessions_staff_id_idx ON staff_sessions (staff_id);

-- staff_name is a copy, so the log still reads after a rename. target_user_id is the app
-- account the action was about, target any other object (report, gateway, staff member).
CREATE TABLE staff_audit (
    id             bigserial PRIMARY KEY,
    staff_id       uuid REFERENCES staff (id) ON DELETE SET NULL,
    staff_name     text NOT NULL,
    action         text NOT NULL,
    target_user_id uuid,
    target         text,
    note           text,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX staff_audit_created_idx ON staff_audit (created_at DESC, id DESC);
CREATE INDEX staff_audit_target_user_idx ON staff_audit (target_user_id, created_at DESC)
    WHERE target_user_id IS NOT NULL;
