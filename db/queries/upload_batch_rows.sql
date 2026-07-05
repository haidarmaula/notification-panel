-- ==========================================
-- CREATE
-- ==========================================

-- name: CreateUploadBatchRow :one
INSERT INTO upload_batch_rows (
    batch_id,
    external_id,
    is_valid,
    error_message
)
VALUES (
    sqlc.arg('batch_id'),
    sqlc.arg('external_id'),
    sqlc.arg('is_valid'),
    sqlc.arg('error_message')
)
RETURNING
    id,
    batch_id,
    external_id,
    is_valid,
    error_message,
    created_at;

-- ==========================================
-- LIST
-- ==========================================

-- name: ListUploadBatchRows :many
SELECT
    id,
    batch_id,
    external_id,
    is_valid,
    error_message,
    created_at
FROM upload_batch_rows
WHERE batch_id = sqlc.arg('batch_id')
ORDER BY created_at
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- COUNT
-- ==========================================

-- name: CountUploadBatchRows :one
SELECT COUNT(*)
FROM upload_batch_rows
WHERE batch_id = sqlc.arg('batch_id');
