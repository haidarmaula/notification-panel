-- name: LockReceiptDelivery :one
SELECT id,attempt_count,status FROM notification_delivery_intents
WHERE application_id=sqlc.arg(application_id)::bigint AND notification_id=sqlc.arg(notification_id)::bigint
    AND id=sqlc.arg(delivery_id) FOR UPDATE;

-- name: FreezeReceiptMetadata :exec
INSERT INTO notification_receipt_metadata(application_id,delivery_id,version,key_id,environment,expires_unix)
SELECT d.application_id,d.id,1,sqlc.arg(key_id),sqlc.arg(environment),
    ceil(extract(epoch FROM n.deadline_at))::bigint+sqlc.arg(window_seconds)::bigint
FROM notification_delivery_intents d JOIN notifications n ON n.application_id=d.application_id AND n.id=d.notification_id
WHERE d.application_id=sqlc.arg(application_id)::bigint AND d.notification_id=sqlc.arg(notification_id)::bigint
    AND d.id=sqlc.arg(delivery_id) AND d.status='PENDING' AND d.attempt_count=0
    AND extract(epoch FROM n.deadline_at)>0
    AND ceil(extract(epoch FROM n.deadline_at))<=253402300799-sqlc.arg(window_seconds)::bigint
ON CONFLICT(delivery_id) DO NOTHING;

-- name: GetFrozenReceiptMetadata :one
SELECT m.*,coalesce(m.expires_unix>=ceil(extract(epoch FROM n.deadline_at)),false)::boolean AS covers_deadline
FROM notification_receipt_metadata m JOIN notification_delivery_intents d ON d.application_id=m.application_id AND d.id=m.delivery_id
JOIN notifications n ON n.application_id=d.application_id AND n.id=d.notification_id
WHERE m.application_id=sqlc.arg(application_id)::bigint AND m.delivery_id=sqlc.arg(delivery_id) FOR SHARE OF m;
