CREATE TABLE segments (
    id BIGSERIAL PRIMARY KEY,

    name VARCHAR(255) NOT NULL,

    description TEXT,

    created_by BIGINT NOT NULL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT fk_segments_admin
        FOREIGN KEY (created_by)
        REFERENCES admin_users(id)
        ON DELETE RESTRICT
);

CREATE INDEX idx_segments_created_by
ON segments(created_by);

CREATE INDEX idx_segments_name
ON segments(name);
