-- name: ListRecoverableQueuedCampaigns :many
SELECT n.id,a.public_id AS application_public_id,n.queued_at,n.scheduled_at,n.deadline_at,n.origin_epoch,n.source_epoch
FROM notifications n JOIN applications a ON a.id=n.application_id
JOIN notification_runtime r ON r.singleton AND NOT r.sealed
  AND n.origin_epoch=r.origin_epoch AND n.source_epoch=r.source_epoch
WHERE n.status='QUEUED' AND n.queued_at IS NOT NULL AND n.deadline_at IS NOT NULL
  AND (n.preparation_next_attempt_at IS NULL OR n.preparation_next_attempt_at<=clock_timestamp())
ORDER BY coalesce(n.preparation_next_attempt_at,n.queued_at),n.id
LIMIT sqlc.arg(page_size)::integer;
