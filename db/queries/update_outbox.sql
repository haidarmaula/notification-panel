-- name: LockNextDeliveryUpdate :one
-- Pick and lock one eligible event in due order before fetching its delivery.
-- The foreign keys guarantee that its application and intent both exist.
WITH picked AS MATERIALIZED (
    SELECT o.* FROM notification_update_outbox o
    WHERE o.next_attempt_at<=statement_timestamp()
        AND (sqlc.narg(application_public_id)::uuid IS NULL OR o.application_id=(
            SELECT a.id FROM applications a WHERE a.public_id=sqlc.narg(application_public_id)::uuid))
        AND o.origin_epoch=sqlc.arg(origin_epoch)::uuid AND o.source_epoch=sqlc.arg(source_epoch)::uuid
        AND NOT EXISTS(SELECT 1 FROM notification_update_outbox predecessor
            WHERE predecessor.delivery_id=o.delivery_id AND predecessor.delivery_version<o.delivery_version)
    ORDER BY o.next_attempt_at,o.event_id LIMIT 1 FOR UPDATE OF o SKIP LOCKED
)
SELECT picked.*,a.public_id AS application_public_id,d.owner_user_id
FROM picked JOIN applications a ON a.id=picked.application_id
JOIN notification_delivery_intents d ON d.application_id=picked.application_id AND d.id=picked.delivery_id;

-- name: DeletePublishedDeliveryUpdate :execrows
DELETE FROM notification_update_outbox WHERE event_id=sqlc.arg(event_id);

-- name: DeferDeliveryUpdate :execrows
UPDATE notification_update_outbox SET attempts=LEAST(attempts,2147483646)+1,
    next_attempt_at=clock_timestamp()+make_interval(secs=>sqlc.arg(delay_seconds)::double precision)
WHERE event_id=sqlc.arg(event_id);

-- name: HasLineageBlockedDeliveryUpdate :one
SELECT EXISTS(SELECT 1 FROM notification_update_outbox o JOIN applications a ON a.id=o.application_id
WHERE o.next_attempt_at<=clock_timestamp()
    AND (sqlc.narg(application_public_id)::uuid IS NULL OR a.public_id=sqlc.narg(application_public_id)::uuid)
    AND o.origin_epoch=sqlc.arg(origin_epoch)::uuid AND o.source_epoch=sqlc.arg(source_epoch)::uuid
    AND EXISTS(SELECT 1 FROM notification_update_outbox predecessor
        WHERE predecessor.delivery_id=o.delivery_id AND predecessor.delivery_version<o.delivery_version
            AND (predecessor.origin_epoch<>o.origin_epoch OR predecessor.source_epoch<>o.source_epoch)))::boolean;
