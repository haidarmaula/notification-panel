-- name: ListRecoverableQueuedCampaigns :many
WITH horizon AS (
    SELECT coalesce(nullif(sqlc.arg(through_id)::bigint,0),(SELECT max(id) FROM notifications),0)::bigint AS pass_end
)
SELECT n.id,a.public_id AS application_public_id,n.queued_at,n.scheduled_at,n.deadline_at,n.origin_epoch,n.source_epoch,horizon.pass_end
FROM notifications n JOIN applications a ON a.id=n.application_id
JOIN notification_runtime r ON r.singleton AND NOT r.sealed
  AND n.origin_epoch=r.origin_epoch AND n.source_epoch=r.source_epoch
CROSS JOIN horizon
WHERE n.status='QUEUED' AND n.queued_at IS NOT NULL AND n.deadline_at IS NOT NULL
  AND n.id>sqlc.arg(after_id)::bigint AND n.id<=horizon.pass_end
  AND (n.preparation_next_attempt_at IS NULL OR n.preparation_next_attempt_at<=clock_timestamp())
ORDER BY n.id
LIMIT sqlc.arg(page_size)::integer;
