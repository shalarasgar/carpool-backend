ALTER TABLE sms_queue DROP CONSTRAINT IF EXISTS sms_queue_delivery_status_check;

ALTER TABLE sms_queue
    DROP COLUMN IF EXISTS delivery_status,
    DROP COLUMN IF EXISTS telephony_accepted_at,
    DROP COLUMN IF EXISTS send_started_at;
