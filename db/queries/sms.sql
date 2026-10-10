-- name: CreateSMSGateway :one
INSERT INTO sms_gateways (name, api_key_hash, operator_prefixes)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetSMSGatewayByKeyHash :one
SELECT *
FROM sms_gateways
WHERE api_key_hash = $1 AND is_active;

-- name: TouchSMSGatewayHeartbeat :exec
UPDATE sms_gateways
SET last_heartbeat_at = now()
WHERE id = $1;

-- name: SelectSMSGatewayForPhone :one
-- Picks a healthy gateway for a phone. Gateways whose operator_prefixes match
-- the national number win over gateways with no prefixes; gateways with
-- non-matching prefixes are skipped. national_number is the part after +993.
SELECT id
FROM sms_gateways
WHERE is_active
  AND last_heartbeat_at IS NOT NULL
  AND last_heartbeat_at > sqlc.arg(since)
  AND (
      cardinality(operator_prefixes) = 0
      OR EXISTS (
          SELECT 1 FROM unnest(operator_prefixes) AS p
          WHERE sqlc.arg(national_number)::text LIKE p || '%'
      )
  )
ORDER BY (cardinality(operator_prefixes) > 0) DESC, last_heartbeat_at DESC
LIMIT 1;

-- name: EnqueueSMS :one
INSERT INTO sms_queue (phone, message, gateway_id)
VALUES ($1, $2, $3)
RETURNING id;

-- name: ClaimPendingSMS :many
-- Atomically claims up to batch_size pending messages for a gateway. Rows
-- locked by a concurrent claimer are skipped, so two gateways never receive
-- the same message.
WITH claimed AS (
    SELECT id
    FROM sms_queue
    WHERE status = 'pending'
      AND (gateway_id IS NULL OR gateway_id = sqlc.arg(gateway_id))
    ORDER BY created_at
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE SKIP LOCKED
)
UPDATE sms_queue AS q
SET status = 'processing',
    gateway_id = sqlc.arg(gateway_id),
    locked_until = now() + (sqlc.arg(lock_seconds)::int * interval '1 second')
FROM claimed
WHERE q.id = claimed.id
RETURNING q.id, q.phone, q.message, q.attempts, q.locked_until, q.created_at;

-- name: MarkSMSSent :execrows
UPDATE sms_queue
SET status = 'sent',
    sent_at = now(),
    locked_until = NULL,
    last_error = NULL
WHERE id = $1
  AND gateway_id = $2
  AND status IN ('processing', 'pending');

-- name: MarkSMSFailed :one
-- Re-queues the message until max_attempts is reached, then marks it failed.
UPDATE sms_queue
SET attempts = attempts + 1,
    last_error = sqlc.narg(last_error),
    locked_until = NULL,
    status = CASE
        WHEN attempts + 1 >= sqlc.arg(max_attempts)::int THEN 'failed'::sms_status
        ELSE 'pending'::sms_status
    END
WHERE id = $1
  AND gateway_id = $2
  AND status IN ('processing', 'pending')
RETURNING status, attempts;

-- name: RequeueStuckSMS :execrows
UPDATE sms_queue
SET status = 'pending',
    locked_until = NULL
WHERE status = 'processing'
  AND locked_until < now();

-- name: UnassignSMSFromStaleGateways :execrows
-- Pending messages routed to a gateway that stopped heartbeating become
-- claimable by any gateway.
UPDATE sms_queue
SET gateway_id = NULL
WHERE status = 'pending'
  AND gateway_id IS NOT NULL
  AND gateway_id IN (
      SELECT id FROM sms_gateways
      WHERE NOT is_active
         OR last_heartbeat_at IS NULL
         OR last_heartbeat_at < sqlc.arg(stale_before)
  );

-- name: GetSMSByID :one
SELECT * FROM sms_queue WHERE id = $1;
