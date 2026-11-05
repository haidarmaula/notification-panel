-- name: LockNotificationRuntime :one
SELECT origin_epoch,source_epoch,sealed,migration_hold FROM notification_runtime WHERE singleton FOR SHARE;

-- name: GetApplicationDispatchPolicy :one
SELECT * FROM application_dispatch_policy WHERE application_id=sqlc.arg(application_id)::bigint FOR SHARE;

-- name: GetSendKey :one
SELECT *, expires_at > clock_timestamp() AS unexpired FROM notification_send_keys
WHERE application_id=sqlc.arg(application_id)::bigint AND staff_user_id=sqlc.arg(staff_user_id)::bigint
  AND key_source=sqlc.arg(key_source) AND key=sqlc.arg(key) FOR UPDATE;

-- name: DeleteSendKey :exec
DELETE FROM notification_send_keys
WHERE application_id=sqlc.arg(application_id)::bigint AND staff_user_id=sqlc.arg(staff_user_id)::bigint
  AND key_source=sqlc.arg(key_source) AND key=sqlc.arg(key);

-- name: LockCampaignAcceptance :one
SELECT id,status,scheduled_at,queued_at,deadline_at,origin_epoch,source_epoch,
       coalesce(scheduled_at <= clock_timestamp(),false)::boolean AS due,
       coalesce(deadline_at <= clock_timestamp(),false)::boolean AS expired
FROM notifications
WHERE application_id=sqlc.arg(application_id)::bigint AND id=sqlc.arg(campaign_id)::bigint FOR UPDATE;

-- name: CampaignAcceptanceReady :one
WITH targets AS (
    SELECT * FROM notification_targets WHERE notification_id=sqlc.arg(campaign_id)::bigint
), eligible AS (
    SELECT u.id FROM users u
    WHERE u.application_id=sqlc.arg(application_id)::bigint AND u.status='ACTIVE'
    AND EXISTS(SELECT 1 FROM targets t WHERE t.application_id=u.application_id AND
        (t.target_type='BROADCAST' OR (t.target_type='INDIVIDUAL' AND t.user_id=u.id)
         OR (t.target_type='SEGMENT' AND EXISTS(SELECT 1 FROM segment_members sm
             WHERE sm.application_id=u.application_id AND sm.segment_id=t.segment_id AND sm.user_id=u.id))))
)
SELECT (EXISTS(SELECT 1 FROM targets)
    AND NOT EXISTS(SELECT 1 FROM targets WHERE application_id IS DISTINCT FROM sqlc.arg(application_id)::bigint
        OR NOT ((target_type='BROADCAST' AND user_id IS NULL AND segment_id IS NULL AND upload_batch_id IS NULL)
            OR (target_type='SEGMENT' AND user_id IS NULL AND segment_id IS NOT NULL AND upload_batch_id IS NULL)
            OR (target_type='INDIVIDUAL' AND user_id IS NOT NULL AND segment_id IS NULL AND upload_batch_id IS NULL)))
    AND NOT EXISTS(SELECT 1 FROM eligible e WHERE
        NOT EXISTS(SELECT 1 FROM installation_bindings b JOIN mobile_installations i
            ON i.application_id=b.application_id AND i.id=b.installation_id
            AND i.owner_user_id=b.owner_user_id AND i.owner_epoch=b.owner_epoch
            WHERE b.application_id=sqlc.arg(application_id)::bigint AND b.owner_user_id=e.id AND b.retired_at IS NULL)
        OR EXISTS(SELECT 1 FROM device_tokens d WHERE d.user_id=e.id AND d.is_active))
)::boolean AS ready;

-- name: QueueScopedCampaign :one
UPDATE notifications SET status='QUEUED', queued_at=clock_timestamp(),
    deadline_at=clock_timestamp()+make_interval(secs=>sqlc.arg(push_ttl_seconds)::integer),
    origin_epoch=sqlc.arg(origin_epoch),source_epoch=sqlc.arg(source_epoch)
WHERE application_id=sqlc.arg(application_id)::bigint AND id=sqlc.arg(campaign_id)::bigint AND status='DRAFT'
RETURNING id,queued_at,deadline_at;

-- name: StoreSendKey :exec
INSERT INTO notification_send_keys(application_id,staff_user_id,key_source,key,notification_id,queued_at,deadline_at,expires_at)
VALUES(sqlc.arg(application_id),sqlc.arg(staff_user_id),sqlc.arg(key_source),sqlc.arg(key),sqlc.arg(notification_id),
    sqlc.arg(queued_at),sqlc.arg(deadline_at),clock_timestamp()+make_interval(secs=>sqlc.arg(key_ttl_seconds)::integer));

-- name: InsertSendOutbox :exec
INSERT INTO notification_send_outbox(application_id,notification_id,requested_at,scheduled_for,deadline_at,origin_epoch,source_epoch)
VALUES(sqlc.arg(application_id),sqlc.arg(notification_id),sqlc.arg(requested_at),sqlc.narg(scheduled_for),
    sqlc.arg(deadline_at),sqlc.arg(origin_epoch),sqlc.arg(source_epoch));

-- name: ScheduleScopedCampaign :one
UPDATE notifications SET status='SCHEDULED',scheduled_at=sqlc.arg(scheduled_at),
    deadline_at=sqlc.arg(scheduled_at)::timestamptz+make_interval(secs=>sqlc.arg(push_ttl_seconds)::integer),
    origin_epoch=sqlc.arg(origin_epoch),source_epoch=sqlc.arg(source_epoch),updated_at=clock_timestamp()
WHERE application_id=sqlc.arg(application_id)::bigint AND id=sqlc.arg(campaign_id)::bigint AND status='DRAFT'
  AND sqlc.arg(scheduled_at)::timestamptz > clock_timestamp()
RETURNING id,scheduled_at,deadline_at;

-- name: PromoteScopedCampaign :one
UPDATE notifications SET status='QUEUED',queued_at=clock_timestamp()
WHERE application_id=sqlc.arg(application_id)::bigint AND id=sqlc.arg(campaign_id)::bigint
  AND status='SCHEDULED' AND scheduled_at<=clock_timestamp() AND deadline_at>clock_timestamp()
RETURNING id,queued_at,deadline_at,scheduled_at,origin_epoch,source_epoch;

-- name: ExpireScopedSchedule :execrows
UPDATE notifications SET status='FAILED',completed_at=clock_timestamp(),lifecycle_reason='ttl_expired'
WHERE application_id=sqlc.arg(application_id)::bigint AND id=sqlc.arg(campaign_id)::bigint
  AND status='SCHEDULED' AND deadline_at<=clock_timestamp();

-- name: ListDueScopedSchedules :many
SELECT n.application_id,n.id,a.public_id AS application_public_id
FROM notifications n
JOIN applications a ON a.id=n.application_id
JOIN notification_runtime r ON r.singleton
WHERE n.status='SCHEDULED' AND n.scheduled_at<=clock_timestamp()
  AND n.deadline_at IS NOT NULL AND NOT r.sealed
  AND n.origin_epoch=r.origin_epoch AND n.source_epoch=r.source_epoch
ORDER BY n.scheduled_at,n.id LIMIT sqlc.arg(page_size)::integer;

-- name: LockAcceptanceStaff :one
SELECT id FROM staff_users WHERE id=sqlc.arg(staff_id)::bigint FOR SHARE;
