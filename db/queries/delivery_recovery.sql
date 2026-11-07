-- name: LockRecoveryDeliveryUser :one
SELECT d.installation_id, d.binding_id, d.owner_user_id, d.owner_epoch, u.status AS user_status
FROM notification_delivery_intents d
JOIN users u ON u.application_id = d.application_id AND u.id = d.owner_user_id
WHERE d.application_id = sqlc.arg(application_id)::bigint AND d.notification_id = sqlc.arg(notification_id)::bigint
  AND d.id = sqlc.arg(delivery_id)
FOR SHARE OF u;

-- name: LockRecoveryInstallationOwner :one
SELECT owner_user_id, owner_epoch FROM mobile_installations
WHERE application_id = sqlc.arg(application_id)::bigint AND id = sqlc.arg(installation_id)::bigint
FOR SHARE;

-- name: LockRecoveryBindingRetirement :one
SELECT retired_at FROM installation_bindings
WHERE application_id = sqlc.arg(application_id)::bigint AND id = sqlc.arg(binding_id)::bigint
  AND installation_id = sqlc.arg(installation_id)::bigint AND owner_user_id = sqlc.arg(owner_user_id)::bigint
  AND owner_epoch = sqlc.arg(owner_epoch)::bigint
FOR SHARE;

-- name: LockRecoverableDelivery :one
SELECT d.status, d.attempt_count, d.attempt_id, d.claim_generation, d.attempt_resolved_at,
       d.delivery_version, d.next_attempt_at, n.deadline_at, clock_timestamp()::timestamptz AS recorded_at
FROM notification_delivery_intents d
JOIN notifications n ON n.application_id = d.application_id AND n.id = d.notification_id
WHERE d.application_id = sqlc.arg(application_id)::bigint AND d.notification_id = sqlc.arg(notification_id)::bigint
  AND d.id = sqlc.arg(delivery_id) AND n.status = 'PROCESSING'
  AND n.origin_epoch = sqlc.arg(origin_epoch) AND n.source_epoch = sqlc.arg(source_epoch)
  AND n.lease_owner = sqlc.arg(owner) AND n.lease_generation = sqlc.arg(generation)::bigint
  AND n.lease_until > clock_timestamp()
FOR UPDATE OF d;

-- name: FailRecoveredDelivery :one
UPDATE notification_delivery_intents d
SET status = 'FAILED', failed_at = sqlc.arg(recorded_at)::timestamptz, terminal_reason = sqlc.arg(reason)::text,
    delivery_version = d.delivery_version + 1
WHERE d.application_id = sqlc.arg(application_id)::bigint AND d.notification_id = sqlc.arg(notification_id)::bigint
  AND d.id = sqlc.arg(delivery_id) AND d.status = 'PENDING'
  AND (d.attempt_id IS NULL OR d.attempt_resolved_at IS NOT NULL OR d.claim_generation < sqlc.arg(generation)::bigint)
  AND EXISTS (
      SELECT 1 FROM notifications n WHERE n.application_id = d.application_id AND n.id = d.notification_id
        AND n.status = 'PROCESSING' AND n.origin_epoch = sqlc.arg(origin_epoch) AND n.source_epoch = sqlc.arg(source_epoch)
        AND n.lease_owner = sqlc.arg(owner) AND n.lease_generation = sqlc.arg(generation)::bigint
        AND n.lease_until > clock_timestamp()
  )
RETURNING *;
