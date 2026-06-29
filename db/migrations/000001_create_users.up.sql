CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,

    external_id VARCHAR(100) NOT NULL UNIQUE,

    name VARCHAR(255),

    email VARCHAR(255),

    status VARCHAR(30) NOT NULL DEFAULT 'ACTIVE'
        CHECK (status IN ('ACTIVE', 'INACTIVE')),

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_users_external_id
ON users(external_id);

CREATE INDEX idx_users_email
ON users(email);

CREATE INDEX idx_users_status
ON users(status);
