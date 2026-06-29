CREATE TABLE templates (
    id BIGSERIAL PRIMARY KEY,

    name VARCHAR(255) NOT NULL,

    title_template TEXT NOT NULL,

    body_template TEXT NOT NULL,

    variables JSONB NOT NULL DEFAULT '[]'::jsonb,

    created_by BIGINT NOT NULL,

    is_active BOOLEAN NOT NULL DEFAULT TRUE,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT fk_templates_admin
        FOREIGN KEY (created_by)
        REFERENCES admin_users(id)
        ON DELETE RESTRICT
);

CREATE INDEX idx_templates_created_by
ON templates(created_by);

CREATE INDEX idx_templates_active
ON templates(is_active);

CREATE INDEX idx_templates_name
ON templates(name);
