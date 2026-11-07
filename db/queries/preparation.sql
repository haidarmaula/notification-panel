-- name: LockPreparationCampaign :one
SELECT id,status,queued_at,scheduled_at,deadline_at,origin_epoch,source_epoch,coalesce(deadline_at<=clock_timestamp(),false)::boolean AS expired FROM notifications
WHERE application_id=sqlc.arg(application_id)::bigint AND id=sqlc.arg(notification_id)::bigint FOR UPDATE;

-- name: GetCompletePreparation :one
SELECT * FROM notification_preparations WHERE application_id=sqlc.arg(application_id)::bigint
    AND notification_id=sqlc.arg(notification_id)::bigint;

-- name: HoldCampaignPreparation :exec
UPDATE notifications SET preparation_hold_reason=sqlc.arg(reason),
    preparation_next_attempt_at=LEAST(deadline_at,clock_timestamp()+interval '30 seconds')
WHERE application_id=sqlc.arg(application_id)::bigint AND id=sqlc.arg(notification_id)::bigint AND status='QUEUED';

-- name: ExpireUnpreparedCampaign :exec
UPDATE notifications SET status='FAILED',completed_at=clock_timestamp(),lifecycle_reason='ttl_expired',
    preparation_hold_reason=NULL,preparation_next_attempt_at=NULL
WHERE application_id=sqlc.arg(application_id)::bigint AND id=sqlc.arg(notification_id)::bigint AND status='QUEUED';

-- name: InsertPreparationIntents :one
WITH targets AS MATERIALIZED (
    SELECT * FROM notification_targets WHERE notification_id=sqlc.arg(notification_id)::bigint
), resolved AS MATERIALIZED (
    SELECT u.id,u.status FROM users u WHERE u.application_id=sqlc.arg(application_id)::bigint
      AND EXISTS(SELECT 1 FROM targets t WHERE t.application_id=u.application_id AND
        (t.target_type='BROADCAST' OR (t.target_type='INDIVIDUAL' AND t.user_id=u.id)
         OR (t.target_type='SEGMENT' AND EXISTS(SELECT 1 FROM segment_members sm
            WHERE sm.application_id=u.application_id AND sm.segment_id=t.segment_id AND sm.user_id=u.id))))
), eligible AS MATERIALIZED (
    SELECT id FROM resolved WHERE status='ACTIVE'
), bindings AS MATERIALIZED (
    SELECT b.application_id,b.id,b.installation_id,b.owner_user_id,b.owner_epoch,
        CASE WHEN r.destination='FCM_ACTIVE' AND (r.origin_epoch,r.source_epoch)=(runtime.origin_epoch,runtime.source_epoch) THEN r.revision END AS route_revision
    FROM installation_bindings b JOIN mobile_installations i
      ON i.application_id=b.application_id AND i.id=b.installation_id
      AND i.owner_user_id=b.owner_user_id AND i.owner_epoch=b.owner_epoch
    JOIN eligible e ON e.id=b.owner_user_id
    CROSS JOIN notification_runtime runtime
    LEFT JOIN LATERAL (SELECT revision,destination,origin_epoch,source_epoch FROM current_binding_routes cr
        WHERE (cr.application_id,cr.binding_id)=(b.application_id,b.id) LIMIT 1) r ON true
    WHERE runtime.singleton AND b.application_id=sqlc.arg(application_id)::bigint AND b.retired_at IS NULL
), assessment AS MATERIALIZED (
    SELECT (EXISTS(SELECT 1 FROM targets) AND NOT EXISTS(SELECT 1 FROM targets
        WHERE application_id IS DISTINCT FROM sqlc.arg(application_id)::bigint
          OR NOT ((target_type='BROADCAST' AND user_id IS NULL AND segment_id IS NULL AND upload_batch_id IS NULL)
              OR (target_type='SEGMENT' AND user_id IS NULL AND segment_id IS NOT NULL AND upload_batch_id IS NULL)
              OR (target_type='INDIVIDUAL' AND user_id IS NOT NULL AND segment_id IS NULL AND upload_batch_id IS NULL))))::boolean AS supported,
      (NOT EXISTS(SELECT 1 FROM bindings WHERE route_revision IS NULL) AND NOT EXISTS(SELECT 1 FROM eligible e WHERE NOT EXISTS(SELECT 1 FROM bindings b WHERE b.owner_user_id=e.id)
          OR EXISTS(SELECT 1 FROM device_tokens d WHERE d.user_id=e.id AND d.is_active)))::boolean AS covered
), inserted AS (
    INSERT INTO notification_delivery_intents(application_id,notification_id,installation_id,binding_id,owner_user_id,owner_epoch,audience_route_revision)
    SELECT b.application_id,sqlc.arg(notification_id)::bigint,b.installation_id,b.id,b.owner_user_id,b.owner_epoch,b.route_revision
    FROM bindings b,assessment a WHERE a.supported AND a.covered
    RETURNING id
)
SELECT a.supported,a.covered,(SELECT count(*) FROM resolved)::bigint AS resolved_users,
    (SELECT count(*) FROM eligible)::bigint AS eligible_users,(SELECT count(*) FROM inserted)::bigint AS eligible_devices
FROM assessment a;

-- name: CompleteCampaignPreparation :one
INSERT INTO notification_preparations(application_id,notification_id,origin_epoch,source_epoch,
    resolved_user_count,eligible_user_count,eligible_device_count)
VALUES(sqlc.arg(application_id),sqlc.arg(notification_id),sqlc.arg(origin_epoch),sqlc.arg(source_epoch),
    sqlc.arg(resolved_users),sqlc.arg(eligible_users),sqlc.arg(eligible_devices)) RETURNING *;

-- name: MarkCampaignPrepared :exec
UPDATE notifications SET status=CASE WHEN sqlc.arg(empty_audience)::boolean THEN 'COMPLETED' ELSE 'PROCESSING' END,
    completed_at=CASE WHEN sqlc.arg(empty_audience)::boolean THEN clock_timestamp() ELSE NULL END,
    preparation_hold_reason=NULL,preparation_next_attempt_at=NULL
WHERE application_id=sqlc.arg(application_id)::bigint AND id=sqlc.arg(notification_id)::bigint AND status='QUEUED';
