-- name: HasHealthySMSGateway :one
SELECT EXISTS (
    SELECT 1
    FROM sms_gateways
    WHERE is_active
      AND last_heartbeat_at IS NOT NULL
      AND last_heartbeat_at > sqlc.arg(since)
) AS healthy;
