-- name: LockNextScopedSendOutbox :one
SELECT o.*,a.public_id AS application_public_id
FROM notification_send_outbox o JOIN applications a ON a.id=o.application_id
WHERE o.next_publish_at<=clock_timestamp()
  AND o.origin_epoch=sqlc.arg(origin_epoch)::uuid AND o.source_epoch=sqlc.arg(source_epoch)::uuid
ORDER BY o.next_publish_at,o.id LIMIT 1 FOR UPDATE OF o SKIP LOCKED;

-- name: DeletePublishedSendOutbox :execrows
DELETE FROM notification_send_outbox WHERE id=sqlc.arg(id)::bigint;

-- name: DeferSendOutbox :exec
UPDATE notification_send_outbox SET publish_attempts=LEAST(publish_attempts,2147483646)+1,
    next_publish_at=clock_timestamp()+make_interval(secs=>sqlc.arg(delay_seconds)::double precision),
    last_error='broker_publish_failed'
WHERE id=sqlc.arg(id)::bigint;
