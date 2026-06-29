CREATE TABLE notification_deliveries (
    id BIGSERIAL PRIMARY KEY,

    notification_id BIGINT NOT NULL,

    user_id BIGINT NOT NULL,

    device_token_id BIGINT NOT NULL,

    provider VARCHAR(20) NOT NULL,

    provider_message_id VARCHAR(255),

    status VARCHAR(20) NOT NULL DEFAULT 'PENDING'
        CHECK (
            status IN (
                'PENDING',
                'SENT',
                'DELIVERED',
                'OPENED',
                'FAILED'
            )
        ),

    retry_count INTEGER NOT NULL DEFAULT 0,

    failed_reason TEXT,

    sent_at TIMESTAMPTZ,

    delivered_at TIMESTAMPTZ,

    opened_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT fk_delivery_notification
        FOREIGN KEY(notification_id)
        REFERENCES notifications(id)
        ON DELETE CASCADE,

    CONSTRAINT fk_delivery_user
        FOREIGN KEY(user_id)
        REFERENCES users(id)
        ON DELETE CASCADE,

    CONSTRAINT fk_delivery_device
        FOREIGN KEY(device_token_id)
        REFERENCES device_tokens(id)
        ON DELETE CASCADE
);

CREATE INDEX idx_delivery_notification
ON notification_deliveries(notification_id);

CREATE INDEX idx_delivery_user
ON notification_deliveries(user_id);

CREATE INDEX idx_delivery_status
ON notification_deliveries(status);

CREATE INDEX idx_delivery_created
ON notification_deliveries(created_at);
