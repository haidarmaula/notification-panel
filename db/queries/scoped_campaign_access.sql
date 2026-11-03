-- name: ListScopedCampaigns :many
SELECT id, title, status
FROM notifications
WHERE application_id = sqlc.arg(application_id)::bigint
  AND id > sqlc.arg(after_id)::bigint
ORDER BY id
LIMIT greatest(0, least(sqlc.arg(page_size)::integer, 101));

-- name: GetScopedCampaign :one
SELECT n.id, n.title, n.body, n.status, n.template_id, n.template_name,
       coalesce((
           SELECT jsonb_agg(jsonb_build_object(
               'type', t.target_type,
               'id', CASE t.target_type
                   WHEN 'SEGMENT' THEN t.segment_id::text
                   WHEN 'INDIVIDUAL' THEN t.user_id::text
                   WHEN 'UPLOAD' THEN t.upload_batch_id::text
               END
           ) ORDER BY t.id)
           FROM notification_targets t
           WHERE t.application_id = n.application_id AND t.notification_id = n.id
       ), '[]'::jsonb)::jsonb AS targets
FROM notifications n
WHERE n.application_id = sqlc.arg(application_id)::bigint
  AND n.id = sqlc.arg(campaign_id)::bigint;

-- name: LockScopedCampaign :one
SELECT status FROM notifications
WHERE application_id = sqlc.arg(application_id)::bigint AND id = sqlc.arg(campaign_id)::bigint
FOR UPDATE;

-- name: CampaignHasProtectedChildren :one
SELECT (
    EXISTS (SELECT 1 FROM notification_deliveries WHERE notification_id = sqlc.arg(campaign_id)::bigint)
    OR EXISTS (SELECT 1 FROM notification_reads WHERE notification_id = sqlc.arg(campaign_id)::bigint)
    OR EXISTS (
        SELECT 1 FROM notification_targets
        WHERE notification_id = sqlc.arg(campaign_id)::bigint
          AND application_id IS DISTINCT FROM sqlc.arg(application_id)::bigint
    )
)::boolean;

-- name: ReplaceScopedCampaignDraft :execrows
UPDATE notifications
SET title = sqlc.arg(title), body = sqlc.arg(body), template_id = sqlc.narg(template_id),
    template_name = sqlc.narg(template_name), updated_at = now()
WHERE application_id = sqlc.arg(application_id)::bigint
  AND id = sqlc.arg(campaign_id)::bigint AND status = 'DRAFT';

-- name: ClearScopedCampaignTargets :exec
DELETE FROM notification_targets
WHERE application_id = sqlc.arg(application_id)::bigint AND notification_id = sqlc.arg(campaign_id)::bigint;

-- name: DeleteScopedCampaignDraft :execrows
DELETE FROM notifications
WHERE application_id = sqlc.arg(application_id)::bigint
  AND id = sqlc.arg(campaign_id)::bigint AND status = 'DRAFT';
