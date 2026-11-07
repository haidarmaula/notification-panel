-- name: LockObservationDelivery :one
SELECT d.id,d.owner_user_id,d.owner_epoch,d.created_at,d.attempt_count,clock_timestamp()::timestamptz AS received_at
FROM notification_delivery_intents d
WHERE d.application_id=sqlc.arg(application_id)::bigint AND d.notification_id=sqlc.arg(notification_id)::bigint
    AND d.id=sqlc.arg(delivery_id)
FOR UPDATE;

-- name: RegisterReceiptRequest :execrows
INSERT INTO notification_receipt_requests(application_id,user_id,client_event_id,delivery_id,fingerprint,expires_unix)
VALUES(sqlc.arg(application_id),sqlc.arg(user_id),sqlc.arg(client_event_id),sqlc.arg(delivery_id),sqlc.arg(fingerprint),sqlc.arg(expires_unix))
ON CONFLICT(application_id,user_id,client_event_id) DO NOTHING;

-- name: GetReceiptFingerprint :one
SELECT fingerprint FROM notification_receipt_requests
WHERE application_id=sqlc.arg(application_id) AND user_id=sqlc.arg(user_id) AND client_event_id=sqlc.arg(client_event_id);

-- name: RecordObservationFact :execrows
INSERT INTO notification_observations(application_id,delivery_id,event,received_at,observed_at)
VALUES(sqlc.arg(application_id),sqlc.arg(delivery_id),sqlc.arg(event),sqlc.arg(received_at),sqlc.narg(observed_at))
ON CONFLICT(delivery_id,event) DO NOTHING;

-- name: ApplyDeliveryObservation :one
UPDATE notification_delivery_intents SET
    observed_opened_at=CASE WHEN sqlc.arg(event)::text='OPENED' THEN coalesce(observed_opened_at,sqlc.arg(received_at)::timestamptz) ELSE observed_opened_at END,
    observed_delivered_at=CASE WHEN sqlc.arg(event)::text='DELIVERED' THEN coalesce(observed_delivered_at,sqlc.arg(received_at)::timestamptz) ELSE observed_delivered_at END,
    status=CASE WHEN status IN ('FAILED','OPENED') THEN status ELSE sqlc.arg(event)::text END,
    delivery_version=delivery_version+1
WHERE application_id=sqlc.arg(application_id)::bigint AND id=sqlc.arg(delivery_id)
    AND ((sqlc.arg(event)::text='OPENED' AND observed_opened_at IS NULL) OR
         (sqlc.arg(event)::text='DELIVERED' AND observed_delivered_at IS NULL))
RETURNING *;

-- name: RecordObservedRead :execrows
INSERT INTO notification_reads(application_id,notification_id,user_id,read_at)
VALUES(sqlc.arg(application_id),sqlc.arg(notification_id),sqlc.arg(user_id),sqlc.arg(received_at))
ON CONFLICT(notification_id,user_id) DO NOTHING;

-- name: CheckScopedObservedRead :one
SELECT EXISTS(SELECT 1 FROM notification_reads WHERE application_id=sqlc.arg(application_id)::bigint
    AND notification_id=sqlc.arg(notification_id)::bigint AND user_id=sqlc.arg(user_id)::bigint);

-- name: GetCampaignStatistics :one
SELECT (p.notification_id IS NOT NULL)::boolean AS snapshot_complete,
    coalesce(p.resolved_user_count,0)::bigint AS targeted_users,
    coalesce(p.eligible_user_count,0)::bigint AS eligible_users,
    coalesce(p.eligible_device_count,0)::bigint AS targeted_devices,
    count(d.id) AS ledger_devices, count(d.id) FILTER (WHERE d.status='PENDING') AS pending_devices,
    count(d.provider_accepted_at) AS provider_accepted_devices,
    count(d.id) FILTER (WHERE d.status='FAILED') AS submission_failed_devices,
    count(d.id) FILTER (WHERE d.observed_delivered_at IS NOT NULL OR d.observed_opened_at IS NOT NULL) AS observed_devices,
    count(d.observed_opened_at) AS opened_devices,
    count(DISTINCT d.owner_user_id) FILTER (WHERE d.observed_opened_at IS NOT NULL) AS opened_users,
    count(d.id) FILTER (WHERE d.status='FAILED' AND (d.observed_opened_at IS NOT NULL OR d.observed_delivered_at IS NOT NULL)) AS failed_observed_devices
FROM notifications n LEFT JOIN notification_preparations p ON p.application_id=n.application_id AND p.notification_id=n.id
    AND p.origin_epoch=n.origin_epoch AND p.source_epoch=n.source_epoch
LEFT JOIN notification_delivery_intents d ON d.application_id=p.application_id AND d.notification_id=p.notification_id
WHERE n.application_id=sqlc.arg(application_id)::bigint AND n.id=sqlc.arg(notification_id)::bigint
GROUP BY n.id,p.notification_id;

-- name: LockReceiptIdentity :one
SELECT user_id FROM mobile_identities
WHERE application_id=sqlc.arg(application_id)::bigint AND identity_namespace=sqlc.arg(identity_namespace)
    AND subject=sqlc.arg(subject) AND user_id=sqlc.arg(user_id)::bigint
    AND clock_timestamp()<sqlc.arg(session_expires_at)::timestamptz
FOR SHARE;
