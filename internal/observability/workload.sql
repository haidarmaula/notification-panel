-- Read-only snapshot. Report schedule and deadline risk even while dispatch is held.
WITH
campaigns AS (
    SELECT status, count(*) AS total
    FROM notifications WHERE application_id IS NOT NULL GROUP BY status
),
deliveries AS (
    SELECT status, count(*) AS total FROM notification_delivery_intents GROUP BY status
),
waiting AS (
    SELECT n.scheduled_at AS since_at, 'schedule' AS queue FROM notifications n
    WHERE n.status='SCHEDULED' AND n.deadline_at IS NOT NULL AND n.scheduled_at <= statement_timestamp()
    UNION ALL
    SELECT n.deadline_at, 'expired_delivery' FROM notifications n
    JOIN notification_delivery_intents d ON d.notification_id=n.id AND d.status='PENDING'
    WHERE n.status='PROCESSING' AND n.deadline_at <= statement_timestamp()
    UNION ALL
    SELECT l.created_at, 'quarantine' FROM notification_send_dead_letters l
    LEFT JOIN notification_replay_controls r USING (log_source_id,original_partition,original_offset)
    WHERE r.resolved_outcome IS NULL
    UNION ALL
    SELECT requested_at, 'send_outbox' FROM notification_send_outbox
    UNION ALL
    SELECT occurred_at, 'update_outbox' FROM notification_update_outbox
)
SELECT 'campaign' AS family, status AS kind, total, 0::float8 AS age FROM campaigns
UNION ALL SELECT 'delivery', status, total, 0 FROM deliveries
UNION ALL
SELECT 'waiting', queue, count(*),
       greatest(0,extract(epoch FROM statement_timestamp()-min(since_at)))::float8
FROM waiting GROUP BY queue
