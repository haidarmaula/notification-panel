-- name: GetScopedCampaignID :one
SELECT id FROM notifications
WHERE application_id = sqlc.arg(application_id)::bigint AND id = sqlc.arg(campaign_id)::bigint;

-- name: ListScopedCampaignDeliveries :many
SELECT d.id, d.user_id, d.device_token_id, d.status, d.sent_at, d.delivered_at, d.opened_at
FROM notification_deliveries d
JOIN notifications n ON n.application_id = d.application_id AND n.id = d.notification_id
JOIN users u ON u.application_id = d.application_id AND u.id = d.user_id
JOIN device_tokens dt ON dt.application_id = d.application_id
    AND dt.id = d.device_token_id AND dt.user_id = d.user_id
WHERE d.application_id = sqlc.arg(application_id)::bigint
  AND d.notification_id = sqlc.arg(campaign_id)::bigint AND d.id > sqlc.arg(after_id)::bigint
ORDER BY d.id
LIMIT greatest(0, least(sqlc.arg(page_size)::integer, 101));

-- name: ListScopedCampaignReads :many
SELECT r.id, r.user_id, r.read_at
FROM notification_reads r
JOIN notifications n ON n.application_id = r.application_id AND n.id = r.notification_id
JOIN users u ON u.application_id = r.application_id AND u.id = r.user_id
WHERE r.application_id = sqlc.arg(application_id)::bigint
  AND r.notification_id = sqlc.arg(campaign_id)::bigint AND r.id > sqlc.arg(after_id)::bigint
ORDER BY r.id
LIMIT greatest(0, least(sqlc.arg(page_size)::integer, 101));
