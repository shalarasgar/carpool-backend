-- Tracks whether a gateway has committed to handing a message to the radio.
-- A lease expiry before send_started_at may return the row to pending.
-- A lease expiry after send_started_at must not be sent again automatically.

ALTER TABLE sms_queue
    ADD COLUMN send_started_at timestamptz,
    ADD COLUMN telephony_accepted_at timestamptz,
    ADD COLUMN delivery_status text;

ALTER TABLE sms_queue
    ADD CONSTRAINT sms_queue_delivery_status_check
    CHECK (
        delivery_status IS NULL
        OR delivery_status IN ('confirmed', 'failed', 'unknown')
    );
