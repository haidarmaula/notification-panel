-- Refuse to erase any assigned business ownership, even on a fixture.

DO $$
BEGIN
    LOCK TABLE users, device_tokens, templates, segments, segment_members, notifications,
        upload_batches, upload_batch_rows, notification_targets, notification_deliveries,
        notification_reads IN ACCESS EXCLUSIVE MODE;
    IF EXISTS(SELECT 1 FROM users WHERE application_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM device_tokens WHERE application_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM templates WHERE application_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM segments WHERE application_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM segment_members WHERE application_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM notifications WHERE application_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM upload_batches WHERE application_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM upload_batch_rows WHERE application_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM notification_targets WHERE application_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM notification_deliveries WHERE application_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM notification_reads WHERE application_id IS NOT NULL) THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'cannot remove assigned application ownership';
    END IF;
    DROP TRIGGER immutable_application_owner ON users;

    DROP TRIGGER immutable_application_owner ON device_tokens;

    DROP TRIGGER immutable_application_owner ON templates;

    DROP TRIGGER immutable_application_owner ON segments;

    DROP TRIGGER immutable_application_owner ON segment_members;

    DROP TRIGGER immutable_application_owner ON notifications;

    DROP TRIGGER immutable_application_owner ON upload_batches;

    DROP TRIGGER immutable_application_owner ON upload_batch_rows;

    DROP TRIGGER immutable_application_owner ON notification_targets;

    DROP TRIGGER immutable_application_owner ON notification_deliveries;

    DROP TRIGGER immutable_application_owner ON notification_reads;

    DROP FUNCTION prevent_business_application_reassignment();

    ALTER TABLE notification_reads DROP CONSTRAINT fk_scope_notification_reads_user_id;

    ALTER TABLE notification_reads DROP CONSTRAINT fk_scope_notification_reads_notification_id;

    ALTER TABLE notification_deliveries DROP CONSTRAINT fk_scope_notification_deliveries_user_id;

    ALTER TABLE notification_deliveries DROP CONSTRAINT fk_scope_notification_deliveries_notification_id;

    ALTER TABLE notification_targets DROP CONSTRAINT fk_scope_notification_targets_upload_batch_id;

    ALTER TABLE notification_targets DROP CONSTRAINT fk_scope_notification_targets_user_id;

    ALTER TABLE notification_targets DROP CONSTRAINT fk_scope_notification_targets_segment_id;

    ALTER TABLE notification_targets DROP CONSTRAINT fk_scope_notification_targets_notification_id;

    ALTER TABLE upload_batch_rows DROP CONSTRAINT fk_scope_upload_batch_rows_batch_id;

    ALTER TABLE notifications DROP CONSTRAINT fk_scope_notifications_template_id;

    ALTER TABLE segment_members DROP CONSTRAINT fk_scope_segment_members_user_id;

    ALTER TABLE segment_members DROP CONSTRAINT fk_scope_segment_members_segment_id;

    ALTER TABLE device_tokens DROP CONSTRAINT fk_scope_device_tokens_user_id;

    ALTER TABLE notification_deliveries DROP CONSTRAINT fk_scope_delivery_device_user;

    ALTER TABLE notification_reads DROP COLUMN application_id;

    ALTER TABLE notification_deliveries DROP COLUMN application_id;

    ALTER TABLE notification_targets DROP COLUMN application_id;

    ALTER TABLE upload_batch_rows DROP COLUMN application_id;

    ALTER TABLE upload_batches DROP COLUMN application_id;

    ALTER TABLE notifications DROP COLUMN application_id;

    ALTER TABLE segment_members DROP COLUMN application_id;

    ALTER TABLE segments DROP COLUMN application_id;

    ALTER TABLE templates DROP COLUMN application_id;

    ALTER TABLE device_tokens DROP COLUMN application_id;

    ALTER TABLE users DROP COLUMN application_id;

END;
$$;
