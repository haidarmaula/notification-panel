-- ==========================================
-- GET
-- ==========================================

-- name: GetUploadBatchByID :one
SELECT
    id,
    uploaded_by,
    original_filename,
    total_rows,
    valid_rows,
    invalid_rows,
    created_at
FROM upload_batches
WHERE id = sqlc.arg('id')
LIMIT 1;

-- ==========================================
-- CREATE
-- ==========================================

-- name: CreateUploadBatch :one
INSERT INTO upload_batches (
    uploaded_by,
    original_filename,
    total_rows,
    valid_rows,
    invalid_rows
)
VALUES (
    sqlc.arg('uploaded_by'),
    sqlc.arg('original_filename'),
    sqlc.arg('total_rows'),
    sqlc.arg('valid_rows'),
    sqlc.arg('invalid_rows')
)
RETURNING
    id,
    uploaded_by,
    original_filename,
    total_rows,
    valid_rows,
    invalid_rows,
    created_at;

-- ==========================================
-- LIST
-- ==========================================

-- name: ListUploadBatches :many
SELECT
    ub.id,
    ub.original_filename,
    ub.total_rows,
    ub.valid_rows,
    ub.invalid_rows,
    su.name AS uploaded_by_name,
    ub.created_at
FROM upload_batches ub
JOIN staff_users su
    ON su.id = ub.uploaded_by
ORDER BY ub.created_at DESC
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- COUNT
-- ==========================================

-- name: CountUploadBatches :one
SELECT COUNT(*)
FROM upload_batches;
