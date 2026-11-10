-- name: GetScopedCampaignID :one
SELECT id FROM notifications
WHERE application_id = sqlc.arg(application_id)::bigint AND id = sqlc.arg(campaign_id)::bigint;

-- name: ListScopedCampaignDeliveries :many
SELECT d.id, d.owner_user_id AS user_id, i.public_id AS installation_id, d.status,
       d.provider_accepted_at AS sent_at, d.observed_delivered_at AS delivered_at,
       d.observed_opened_at AS opened_at
FROM notification_delivery_intents d
JOIN notifications n ON n.application_id = d.application_id AND n.id = d.notification_id
JOIN mobile_installations i ON i.application_id = d.application_id AND i.id = d.installation_id
WHERE d.application_id = sqlc.arg(application_id)::bigint
  AND d.notification_id = sqlc.arg(campaign_id)::bigint AND d.id > sqlc.arg(after_id)::uuid
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
