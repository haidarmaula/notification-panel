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
WHERE id = $1
LIMIT 1;

-- name: GetUploadBatchesByUploadedBy :many
SELECT
    id,
    uploaded_by,
    original_filename,
    total_rows,
    valid_rows,
    invalid_rows,
    created_at
FROM upload_batches
WHERE uploaded_by = $1
ORDER BY created_at DESC;

-- name: CreateUploadBatch :one
INSERT INTO upload_batches (
    uploaded_by,
    original_filename,
    total_rows,
    valid_rows,
    invalid_rows
)
VALUES (
    $1,
    $2,
    $3,
    $4,
    $5
)
RETURNING *;

-- name: UpdateUploadBatchCounts :exec
UPDATE upload_batches
SET
    total_rows = $2,
    valid_rows = $3,
    invalid_rows = $4
WHERE id = $1;

-- name: DeleteUploadBatch :exec
DELETE FROM upload_batches
WHERE id = $1;

-- name: ListUploadBatches :many
SELECT
    id,
    uploaded_by,
    original_filename,
    total_rows,
    valid_rows,
    invalid_rows,
    created_at
FROM upload_batches
WHERE
    ($1::bigint IS NULL OR uploaded_by = $1) AND
    ($2::timestamptz IS NULL OR created_at >= $2) AND
    ($3::timestamptz IS NULL OR created_at <= $3)
ORDER BY created_at DESC;
