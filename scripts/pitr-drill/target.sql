INSERT INTO notifications(id,application_id,title,body,created_by,status,queued_at,deadline_at,origin_epoch,source_epoch)
SELECT 902,901,'archived WAL','synthetic',901,'QUEUED',clock_timestamp(),clock_timestamp()+interval '1 hour',origin_epoch,source_epoch
FROM notification_runtime WHERE singleton;

INSERT INTO notification_send_outbox(application_id,notification_id,requested_at,deadline_at,origin_epoch,source_epoch)
SELECT application_id,id,queued_at,deadline_at,origin_epoch,source_epoch FROM notifications WHERE id=902;
