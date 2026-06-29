CREATE TABLE upload_batch_rows (
    id BIGSERIAL PRIMARY KEY,

    batch_id BIGINT NOT NULL,

    external_id VARCHAR(100) NOT NULL,

    is_valid BOOLEAN NOT NULL DEFAULT TRUE,

    error_message TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT fk_upload_batch_rows_batch
        FOREIGN KEY(batch_id)
        REFERENCES upload_batches(id)
        ON DELETE CASCADE
);

CREATE INDEX idx_upload_batch_rows_batch
ON upload_batch_rows(batch_id);

CREATE INDEX idx_upload_batch_rows_external
ON upload_batch_rows(external_id);

CREATE INDEX idx_upload_batch_rows_valid
ON upload_batch_rows(is_valid);
