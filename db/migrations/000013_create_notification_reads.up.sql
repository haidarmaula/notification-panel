CREATE TABLE notification_reads (
    id BIGSERIAL PRIMARY KEY,

    notification_id BIGINT NOT NULL,

    user_id BIGINT NOT NULL,

    read_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT fk_notification_reads_notification
        FOREIGN KEY(notification_id)
        REFERENCES notifications(id)
        ON DELETE CASCADE,

    CONSTRAINT fk_notification_reads_user
        FOREIGN KEY(user_id)
        REFERENCES users(id)
        ON DELETE CASCADE,

    CONSTRAINT uq_notification_read
        UNIQUE(notification_id, user_id)
);

CREATE INDEX idx_notification_reads_user
ON notification_reads(user_id);

CREATE INDEX idx_notification_reads_notification
ON notification_reads(notification_id);
