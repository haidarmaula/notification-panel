-- name: LockResultPolicy :one
SELECT * FROM application_dispatch_policy WHERE application_id=sqlc.arg(application_id)::bigint FOR UPDATE;

-- name: HoldResultDispatch :exec
SELECT hold_delivery_dispatch(sqlc.arg(application_id)::bigint);

-- name: LockResultInstallation :one
SELECT owner_user_id,owner_epoch,version FROM mobile_installations WHERE application_id=sqlc.arg(application_id)::bigint AND id=sqlc.arg(installation_id)::bigint FOR UPDATE;

-- The worker has no authority to transfer installation ownership.
-- name: AdvanceInvalidatedInstallation :one
UPDATE mobile_installations SET version=version+1,last_seen_at=now()
WHERE application_id=sqlc.arg(application_id)::bigint AND id=sqlc.arg(id)::bigint
  AND version=sqlc.arg(expected_version)::bigint RETURNING version;

-- name: LockResultBinding :one
SELECT * FROM installation_bindings WHERE application_id=sqlc.arg(application_id)::bigint AND id=sqlc.arg(binding_id)::bigint FOR UPDATE;

-- name: LockResultDelivery :one
SELECT d.*,n.deadline_at,clock_timestamp()::timestamptz AS recorded_at
FROM notification_delivery_intents d JOIN notifications n ON n.application_id=d.application_id AND n.id=d.notification_id
WHERE d.application_id=sqlc.arg(application_id)::bigint AND d.notification_id=sqlc.arg(notification_id)::bigint
    AND d.id=sqlc.arg(delivery_id) AND d.attempt_id=sqlc.arg(attempt_id) AND d.claim_generation=sqlc.arg(generation)::bigint
    AND n.status='PROCESSING' AND n.origin_epoch=sqlc.arg(origin_epoch) AND n.source_epoch=sqlc.arg(source_epoch)
    AND n.lease_owner=sqlc.arg(owner) AND n.lease_generation=sqlc.arg(generation)::bigint AND n.lease_until>clock_timestamp()
FOR UPDATE OF d;

-- name: WriteDeliveryResult :one
UPDATE notification_delivery_intents d SET status=sqlc.arg(status),
    provider_message_id=sqlc.narg(provider_message_id),provider_accepted_at=sqlc.narg(provider_accepted_at),
    failed_at=sqlc.narg(failed_at),terminal_reason=sqlc.narg(terminal_reason),
    last_result_disposition=sqlc.arg(disposition),last_result_reason=sqlc.arg(reason),last_result_fingerprint=sqlc.arg(fingerprint),
    attempt_resolved_at=sqlc.arg(recorded_at),next_attempt_at=greatest(d.next_attempt_at,sqlc.arg(next_attempt_at)::timestamptz),
    delivery_version=d.delivery_version+sqlc.arg(version_increment)::bigint
WHERE d.application_id=sqlc.arg(application_id)::bigint AND d.id=sqlc.arg(delivery_id)
    AND d.attempt_id=sqlc.arg(attempt_id) AND d.claim_generation=sqlc.arg(generation)::bigint AND d.attempt_resolved_at IS NULL
    AND EXISTS(SELECT 1 FROM notifications n WHERE n.application_id=d.application_id AND n.id=d.notification_id
        AND n.status='PROCESSING' AND n.origin_epoch=sqlc.arg(origin_epoch) AND n.source_epoch=sqlc.arg(source_epoch)
        AND n.lease_owner=sqlc.arg(owner) AND n.lease_generation=sqlc.arg(generation)::bigint AND n.lease_until>clock_timestamp())
RETURNING *;

-- name: InsertDeliveryUpdate :exec
INSERT INTO notification_update_outbox(event_id,application_id,notification_id,delivery_id,delivery_version,origin_epoch,source_epoch,payload,occurred_at)
VALUES(sqlc.arg(event_id),sqlc.arg(application_id),sqlc.arg(notification_id),sqlc.arg(delivery_id),sqlc.arg(delivery_version),
    sqlc.arg(origin_epoch),sqlc.arg(source_epoch),sqlc.arg(payload),sqlc.arg(occurred_at));
