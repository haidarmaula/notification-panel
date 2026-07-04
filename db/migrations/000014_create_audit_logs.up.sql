CREATE TABLE audit_logs (
    id BIGSERIAL PRIMARY KEY,

    actor_user_id BIGINT NOT NULL,

    action VARCHAR(100) NOT NULL,

    entity_type VARCHAR(100) NOT NULL,

    entity_name VARCHAR(255),

    entity_id BIGINT,

    before_json JSONB,

    after_json JSONB,

    ip_address VARCHAR(100),

    user_agent TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT fk_audit_actor
        FOREIGN KEY(actor_user_id)
        REFERENCES staff_users(id)
        ON DELETE RESTRICT
);

CREATE INDEX idx_audit_actor
ON audit_logs(actor_user_id);

CREATE INDEX idx_audit_entity
ON audit_logs(entity_type, entity_id);

CREATE INDEX idx_audit_created
ON audit_logs(created_at);
