-- name: ListExecutionRecoveryCampaigns :many
-- Metadata only: a later acquisition rechecks the marker, epochs and live lease.
SELECT n.id, a.public_id AS application_public_id, n.origin_epoch, n.source_epoch
FROM notifications n
JOIN applications a ON a.id=n.application_id
JOIN application_dispatch_policy ap ON ap.application_id=a.id
JOIN notification_runtime r ON r.singleton AND NOT r.sealed AND NOT r.migration_hold
    AND r.origin_epoch=n.origin_epoch AND r.source_epoch=n.source_epoch
JOIN notification_preparations p ON p.application_id=a.id AND p.notification_id=n.id
    AND p.origin_epoch=n.origin_epoch AND p.source_epoch=n.source_epoch
WHERE n.status='PROCESSING' AND n.deadline_at IS NOT NULL AND n.id>sqlc.arg(after_id)::bigint
    AND (n.lease_until IS NULL OR n.lease_until<=clock_timestamp())
    AND (NOT EXISTS (SELECT 1 FROM notification_delivery_intents d WHERE d.notification_id=n.id AND d.status='PENDING')
        OR EXISTS (SELECT 1 FROM notification_delivery_intents d WHERE d.notification_id=n.id AND d.status='PENDING'
            AND (n.deadline_at<=clock_timestamp() OR d.next_attempt_at>=n.deadline_at
                OR d.attempt_count>=sqlc.arg(max_attempts)::integer
                OR (d.next_attempt_at<=clock_timestamp() AND a.status='ACTIVE' AND ap.coverage_approved AND NOT ap.hold_new))))
ORDER BY n.id LIMIT sqlc.arg(page_size)::integer;

-- name: ListExecutionRecoveryDevices :many
-- Healthy due devices can be returned as advisory IDs without locking every
-- recipient twice. The send path always checks the actual current ownership.
SELECT d.id, (d.attempt_count>=sqlc.arg(max_attempts)::integer OR n.deadline_at<=clock_timestamp()
    OR d.next_attempt_at>=n.deadline_at OR u.status<>'ACTIVE' OR b.retired_at IS NOT NULL
    OR i.owner_user_id IS DISTINCT FROM d.owner_user_id OR i.owner_epoch<>d.owner_epoch)::boolean AS needs_recovery
FROM notification_delivery_intents d
JOIN notifications n ON n.application_id=d.application_id AND n.id=d.notification_id
JOIN users u ON u.application_id=d.application_id AND u.id=d.owner_user_id
JOIN mobile_installations i ON i.application_id=d.application_id AND i.id=d.installation_id
JOIN installation_bindings b ON b.application_id=d.application_id AND b.id=d.binding_id
WHERE d.application_id=sqlc.arg(application_id)::bigint AND d.notification_id=sqlc.arg(notification_id)::bigint
    AND d.status='PENDING' AND (sqlc.narg(after_id)::uuid IS NULL OR d.id>sqlc.narg(after_id)::uuid)
    AND (d.attempt_id IS NULL OR d.attempt_resolved_at IS NOT NULL OR d.claim_generation<sqlc.arg(generation)::bigint)
    AND (n.deadline_at<=clock_timestamp() OR d.next_attempt_at>=n.deadline_at OR d.attempt_count>=sqlc.arg(max_attempts)::integer
        OR (sqlc.arg(send_allowed)::boolean AND d.next_attempt_at<=clock_timestamp()))
    AND n.lease_owner=sqlc.arg(owner) AND n.lease_generation=sqlc.arg(generation)::bigint AND n.lease_until>clock_timestamp()
    AND n.status='PROCESSING' AND n.origin_epoch=sqlc.arg(origin_epoch) AND n.source_epoch=sqlc.arg(source_epoch)
ORDER BY d.id LIMIT sqlc.arg(page_size)::integer;
