CREATE TABLE device_tokens (
    id BIGSERIAL PRIMARY KEY,

    user_id BIGINT NOT NULL,

    provider VARCHAR(30) NOT NULL
        CHECK (provider IN (
            'FCM',
            'APNS',
            'HUAWEI'
        )),

    platform VARCHAR(30) NOT NULL
        CHECK (platform IN (
            'ANDROID',
            'IOS',
            'WEB'
        )),

    installation_id VARCHAR(255),

    push_token TEXT NOT NULL,

    app_version VARCHAR(50),

    os_version VARCHAR(50),

    device_model VARCHAR(100),

    is_active BOOLEAN NOT NULL DEFAULT TRUE,

    last_seen_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT fk_device_tokens_user
        FOREIGN KEY (user_id)
        REFERENCES users(id)
        ON DELETE CASCADE
);

CREATE INDEX idx_device_tokens_user
ON device_tokens(user_id);

CREATE INDEX idx_device_tokens_active
ON device_tokens(is_active);

CREATE INDEX idx_device_tokens_last_seen
ON device_tokens(last_seen_at);

CREATE UNIQUE INDEX uq_device_push_token
ON device_tokens(push_token);
