CREATE TABLE notifications (

    id BIGSERIAL PRIMARY KEY,

    template_id BIGINT,

    template_name VARCHAR(255),

    title TEXT NOT NULL,

    body TEXT NOT NULL,

    payload JSONB NOT NULL DEFAULT '{}'::jsonb,

    priority VARCHAR(20)
        NOT NULL
        DEFAULT 'NORMAL'
        CHECK (
            priority IN (
                'NORMAL',
                'HIGH'
            )
        ),

    status VARCHAR(30)
        NOT NULL
        DEFAULT 'DRAFT'
        CHECK (
            status IN (
                'DRAFT',
                'SCHEDULED',
                'QUEUED',
                'PROCESSING',
                'COMPLETED',
                'FAILED',
                'CANCELLED'
            )
        ),

    scheduled_at TIMESTAMPTZ,

    published_at TIMESTAMPTZ,

    completed_at TIMESTAMPTZ,

    created_by BIGINT NOT NULL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT fk_notifications_template
        FOREIGN KEY (template_id)
        REFERENCES templates(id)
        ON DELETE SET NULL,

    CONSTRAINT fk_notifications_staff
        FOREIGN KEY (created_by)
        REFERENCES staff_users(id)
        ON DELETE RESTRICT
);

CREATE INDEX idx_notifications_status
ON notifications(status);

CREATE INDEX idx_notifications_priority
ON notifications(priority);

CREATE INDEX idx_notifications_created_by
ON notifications(created_by);

CREATE INDEX idx_notifications_template
ON notifications(template_id);

CREATE INDEX idx_notifications_scheduled
ON notifications(scheduled_at);

CREATE INDEX idx_notifications_created_at
ON notifications(created_at);
