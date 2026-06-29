CREATE TABLE admin_users (
    id BIGSERIAL PRIMARY KEY,

    role_id BIGINT NOT NULL,

    name VARCHAR(255) NOT NULL,

    email VARCHAR(255) NOT NULL UNIQUE,

    password_hash TEXT NOT NULL,

    is_active BOOLEAN NOT NULL DEFAULT TRUE,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT fk_admin_users_role
        FOREIGN KEY (role_id)
        REFERENCES roles(id)
        ON DELETE RESTRICT
);

CREATE INDEX idx_admin_users_role
ON admin_users(role_id);

CREATE INDEX idx_admin_users_email
ON admin_users(email);

CREATE INDEX idx_admin_users_active
ON admin_users(is_active);
