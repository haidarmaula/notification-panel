CREATE TABLE notification_targets (
    id BIGSERIAL PRIMARY KEY,

    notification_id BIGINT NOT NULL,

    target_type VARCHAR(20) NOT NULL
        CHECK (
            target_type IN (
                'GLOBAL',
                'SEGMENT',
                'USER',
                'UPLOAD'
            )
        ),

    segment_id BIGINT,

    user_id BIGINT,

    upload_batch_id BIGINT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT fk_notification_targets_notification
        FOREIGN KEY(notification_id)
        REFERENCES notifications(id)
        ON DELETE CASCADE,

    CONSTRAINT fk_notification_targets_segment
        FOREIGN KEY(segment_id)
        REFERENCES segments(id)
        ON DELETE CASCADE,

    CONSTRAINT fk_notification_targets_user
        FOREIGN KEY(user_id)
        REFERENCES users(id)
        ON DELETE CASCADE,

    CONSTRAINT fk_notification_targets_upload
        FOREIGN KEY(upload_batch_id)
        REFERENCES upload_batches(id)
        ON DELETE CASCADE,

    CONSTRAINT chk_notification_target
    CHECK (
        (target_type='GLOBAL' AND segment_id IS NULL AND user_id IS NULL AND upload_batch_id IS NULL)

        OR

        (target_type='SEGMENT' AND segment_id IS NOT NULL)

        OR

        (target_type='USER' AND user_id IS NOT NULL)

        OR

        (target_type='UPLOAD' AND upload_batch_id IS NOT NULL)
    )
);

CREATE INDEX idx_notification_targets_notification
ON notification_targets(notification_id);

CREATE INDEX idx_notification_targets_segment
ON notification_targets(segment_id);

CREATE INDEX idx_notification_targets_user
ON notification_targets(user_id);

CREATE INDEX idx_notification_targets_upload
ON notification_targets(upload_batch_id);
