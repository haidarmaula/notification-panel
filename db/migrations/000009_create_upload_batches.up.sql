CREATE TABLE upload_batches (
    id BIGSERIAL PRIMARY KEY,

    uploaded_by BIGINT NOT NULL,

    original_filename VARCHAR(255) NOT NULL,

    total_rows INTEGER NOT NULL DEFAULT 0,

    valid_rows INTEGER NOT NULL DEFAULT 0,

    invalid_rows INTEGER NOT NULL DEFAULT 0,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT fk_upload_batches_staff
        FOREIGN KEY (uploaded_by)
        REFERENCES staff_users(id)
        ON DELETE RESTRICT
);

CREATE INDEX idx_upload_batches_uploaded_by
ON upload_batches(uploaded_by);

CREATE INDEX idx_upload_batches_created_at
ON upload_batches(created_at);
