ALTER TABLE notifications
    ADD COLUMN queued_at TIMESTAMPTZ,
    ADD COLUMN deadline_at TIMESTAMPTZ,
    ADD COLUMN origin_epoch UUID,
    ADD COLUMN source_epoch UUID,
    ADD COLUMN lifecycle_reason TEXT;

CREATE TABLE notification_runtime (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    origin_epoch UUID NOT NULL DEFAULT gen_random_uuid(),
    source_epoch UUID NOT NULL DEFAULT gen_random_uuid(),
    sealed BOOLEAN NOT NULL DEFAULT TRUE,
    migration_hold BOOLEAN NOT NULL DEFAULT TRUE
);
INSERT INTO notification_runtime(singleton) VALUES(TRUE);

CREATE TABLE application_dispatch_policy (
    application_id BIGINT PRIMARY KEY REFERENCES applications(id) ON DELETE RESTRICT,
    hold_new BOOLEAN NOT NULL DEFAULT TRUE,
    coverage_approved BOOLEAN NOT NULL DEFAULT FALSE,
    push_ttl_seconds INTEGER NOT NULL CHECK (push_ttl_seconds BETWEEN 1 AND 604800),
    key_ttl_seconds INTEGER NOT NULL CHECK (key_ttl_seconds BETWEEN 1 AND 604800)
);

CREATE TABLE notification_send_keys (
    application_id BIGINT NOT NULL REFERENCES applications(id) ON DELETE RESTRICT,
    staff_user_id BIGINT NOT NULL REFERENCES staff_users(id) ON DELETE RESTRICT,
    key_source TEXT NOT NULL CHECK (key_source IN ('client','derived')),
    key TEXT COLLATE "C" NOT NULL CHECK (octet_length(key) BETWEEN 1 AND 1020),
    notification_id BIGINT NOT NULL,
    queued_at TIMESTAMPTZ NOT NULL,
    deadline_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (application_id, staff_user_id, key_source, key),
    FOREIGN KEY (application_id, notification_id) REFERENCES notifications(application_id,id) ON DELETE RESTRICT,
    CHECK (deadline_at > queued_at AND expires_at > queued_at)
);

CREATE TABLE notification_send_outbox (
    id BIGSERIAL PRIMARY KEY,
    application_id BIGINT NOT NULL,
    notification_id BIGINT NOT NULL UNIQUE,
    requested_at TIMESTAMPTZ NOT NULL,
    scheduled_for TIMESTAMPTZ,
    deadline_at TIMESTAMPTZ NOT NULL,
    origin_epoch UUID NOT NULL,
    source_epoch UUID NOT NULL,
    publish_attempts INTEGER NOT NULL DEFAULT 0 CHECK (publish_attempts >= 0),
    next_publish_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT CHECK (octet_length(last_error) <= 512),
    FOREIGN KEY (application_id, notification_id) REFERENCES notifications(application_id,id) ON DELETE RESTRICT
);
CREATE INDEX idx_notification_send_outbox_due ON notification_send_outbox(next_publish_at,id);
CREATE INDEX idx_scoped_schedules_due ON notifications(application_id,scheduled_at,id)
    WHERE status='SCHEDULED' AND deadline_at IS NOT NULL;
