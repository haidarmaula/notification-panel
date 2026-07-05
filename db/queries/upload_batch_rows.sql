-- name: GetUploadBatchRowByID :one
SELECT
    id,
    batch_id,
    external_id,
    is_valid,
    error_message,
    created_at
FROM upload_batch_rows
WHERE id = $1
LIMIT 1;

-- name: GetUploadBatchRowsByBatchID :many
SELECT
    id,
    batch_id,
    external_id,
    is_valid,
    error_message,
    created_at
FROM upload_batch_rows
WHERE batch_id = $1
ORDER BY id;

-- name: GetUploadBatchRowsByExternalID :many
SELECT
    id,
    batch_id,
    external_id,
    is_valid,
    error_message,
    created_at
FROM upload_batch_rows
WHERE external_id = $1
ORDER BY batch_id DESC;

-- name: CreateUploadBatchRow :one
INSERT INTO upload_batch_rows (
    batch_id,
    external_id,
    is_valid,
    error_message
)
VALUES (
    $1,
    $2,
    $3,
    $4
)
RETURNING *;

-- name: UpdateUploadBatchRow :exec
UPDATE upload_batch_rows
SET
    is_valid = $2,
    error_message = $3
WHERE id = $1;

-- name: DeleteUploadBatchRow :exec
DELETE FROM upload_batch_rows
WHERE id = $1;

-- name: DeleteUploadBatchRowsByBatch :exec
DELETE FROM upload_batch_rows
WHERE batch_id = $1;

-- name: ListUploadBatchRows :many
SELECT
    id,
    batch_id,
    external_id,
    is_valid,
    error_message,
    created_at
FROM upload_batch_rows
WHERE
    ($1::bigint IS NULL OR batch_id = $1) AND
    ($2::boolean IS NULL OR is_valid = $2)
ORDER BY id;
