-- name: LockExecutionApplication :one
SELECT id,status FROM applications WHERE public_id=sqlc.arg(application_public_id) FOR SHARE;

-- name: AcquireExecutionLease :one
UPDATE notifications n SET lease_owner=sqlc.arg(owner),lease_generation=n.lease_generation+1,
    lease_until=clock_timestamp()+make_interval(secs=>sqlc.arg(lease_seconds)::integer)
WHERE n.application_id=sqlc.arg(application_id)::bigint AND n.id=sqlc.arg(notification_id)::bigint
    AND n.status='PROCESSING' AND n.origin_epoch=sqlc.arg(origin_epoch) AND n.source_epoch=sqlc.arg(source_epoch)
    AND n.lease_generation<9223372036854775807 AND (n.lease_until IS NULL OR n.lease_until<=clock_timestamp())
    AND EXISTS(SELECT 1 FROM notification_delivery_intents d WHERE d.notification_id=n.id AND d.status='PENDING'
        AND (d.next_attempt_at<=clock_timestamp() OR d.attempt_count>=sqlc.arg(max_attempts)::integer OR n.deadline_at<=clock_timestamp()))
RETURNING n.lease_generation,n.lease_until;

-- name: RenewExecutionLease :execrows
UPDATE notifications SET lease_until=clock_timestamp()+make_interval(secs=>sqlc.arg(lease_seconds)::integer)
WHERE application_id=sqlc.arg(application_id)::bigint AND id=sqlc.arg(notification_id)::bigint AND status='PROCESSING'
    AND origin_epoch=sqlc.arg(origin_epoch) AND source_epoch=sqlc.arg(source_epoch)
    AND lease_owner=sqlc.arg(owner) AND lease_generation=sqlc.arg(generation)::bigint AND lease_until>clock_timestamp();

-- name: ReleaseExecutionLease :execrows
UPDATE notifications SET lease_owner=NULL,lease_until=NULL
WHERE application_id=sqlc.arg(application_id)::bigint AND id=sqlc.arg(notification_id)::bigint
    AND origin_epoch=sqlc.arg(origin_epoch) AND source_epoch=sqlc.arg(source_epoch)
    AND lease_owner=sqlc.arg(owner) AND lease_generation=sqlc.arg(generation)::bigint AND lease_until>clock_timestamp();

-- name: GetExecutionIntentIdentity :one
SELECT installation_id,binding_id,owner_user_id,owner_epoch FROM notification_delivery_intents
WHERE application_id=sqlc.arg(application_id)::bigint AND notification_id=sqlc.arg(notification_id)::bigint AND id=sqlc.arg(delivery_id);

-- name: LockExecutionInstallation :one
SELECT * FROM mobile_installations WHERE application_id=sqlc.arg(application_id)::bigint AND id=sqlc.arg(installation_id)::bigint FOR SHARE;

-- name: LockExecutionBinding :one
SELECT * FROM installation_bindings WHERE application_id=sqlc.arg(application_id)::bigint AND id=sqlc.arg(binding_id)::bigint FOR SHARE;

-- name: ReserveExecutionAttempt :one
WITH reservation_clock AS MATERIALIZED (SELECT clock_timestamp() AS reserved_at)
UPDATE notification_delivery_intents d SET attempt_count=d.attempt_count+1,attempt_id=sqlc.arg(attempt_id),
    claim_generation=sqlc.arg(generation)::bigint,last_attempt_at=reservation_clock.reserved_at,attempt_resolved_at=NULL,
    next_attempt_at=reservation_clock.reserved_at+make_interval(secs=>sqlc.arg(retry_floor_seconds)::integer)
FROM reservation_clock
WHERE d.application_id=sqlc.arg(application_id)::bigint AND d.notification_id=sqlc.arg(notification_id)::bigint
    AND d.id=sqlc.arg(delivery_id) AND d.status='PENDING' AND d.attempt_count<sqlc.arg(max_attempts)::integer
    AND d.next_attempt_at<=clock_timestamp()
    AND (d.attempt_id IS NULL OR d.attempt_resolved_at IS NOT NULL OR d.claim_generation<sqlc.arg(generation)::bigint)
    AND EXISTS(SELECT 1 FROM notifications n WHERE n.application_id=d.application_id AND n.id=d.notification_id
        AND n.status='PROCESSING' AND n.deadline_at>clock_timestamp()
        AND n.origin_epoch=sqlc.arg(origin_epoch) AND n.source_epoch=sqlc.arg(source_epoch)
        AND n.lease_owner=sqlc.arg(owner) AND n.lease_generation=sqlc.arg(generation)::bigint AND n.lease_until>clock_timestamp())
RETURNING d.attempt_count,d.last_attempt_at,d.next_attempt_at;

-- name: GetExecutionProviderRoute :one
SELECT b.provider_namespace,b.provider_route_key,b.platform,b.address_type
FROM notification_delivery_intents d JOIN installation_bindings b
    ON b.application_id=d.application_id AND b.id=d.binding_id
WHERE d.application_id=sqlc.arg(application_id)::bigint AND d.notification_id=sqlc.arg(notification_id)::bigint
    AND d.id=sqlc.arg(delivery_id);

-- name: LockExecutionSubmission :one
SELECT n.title,n.body,n.payload,n.deadline_at,
    floor(extract(epoch FROM (least(n.lease_until,n.deadline_at)-clock_timestamp()))*1000000)::bigint AS valid_micros
FROM notification_delivery_intents d JOIN notifications n ON n.application_id=d.application_id AND n.id=d.notification_id
WHERE d.application_id=sqlc.arg(application_id)::bigint AND d.notification_id=sqlc.arg(notification_id)::bigint
    AND d.id=sqlc.arg(delivery_id) AND d.status='PENDING' AND d.attempt_id=sqlc.arg(attempt_id)
    AND d.claim_generation=sqlc.arg(generation)::bigint AND d.attempt_resolved_at IS NULL
    AND d.attempt_count>0 AND d.attempt_count<=sqlc.arg(max_attempts)::integer
    AND n.status='PROCESSING' AND n.origin_epoch=sqlc.arg(origin_epoch) AND n.source_epoch=sqlc.arg(source_epoch)
    AND n.lease_owner=sqlc.arg(owner) AND n.lease_generation=sqlc.arg(generation)::bigint
    AND n.lease_until>clock_timestamp() AND n.deadline_at>clock_timestamp()
    AND octet_length(n.payload::text)<=65536 AND octet_length(n.title)<=4096 AND octet_length(n.body)<=4096
FOR UPDATE OF d;
