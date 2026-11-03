-- name: ListScopedUploadBatches :many
SELECT id, total_rows, valid_rows, invalid_rows, created_at
FROM upload_batches
WHERE application_id = sqlc.arg(application_id)::bigint AND id > sqlc.arg(after_id)::bigint
ORDER BY id
LIMIT greatest(0, least(sqlc.arg(page_size)::integer, 101));

-- name: GetScopedUploadBatchID :one
SELECT id FROM upload_batches
WHERE application_id = sqlc.arg(application_id)::bigint AND id = sqlc.arg(batch_id)::bigint;

-- name: ListScopedUploadRows :many
SELECT r.id, r.is_valid, r.created_at
FROM upload_batch_rows r
JOIN upload_batches b ON b.application_id = r.application_id AND b.id = r.batch_id
WHERE r.application_id = sqlc.arg(application_id)::bigint
  AND r.batch_id = sqlc.arg(batch_id)::bigint AND r.id > sqlc.arg(after_id)::bigint
ORDER BY r.id
LIMIT greatest(0, least(sqlc.arg(page_size)::integer, 101));

-- name: ListScopedAuditLogs :many
SELECT id, actor_user_id, action, entity_type, entity_id, created_at
FROM audit_logs
WHERE application_id = sqlc.arg(application_id)::bigint AND id > sqlc.arg(after_id)::bigint
ORDER BY id
LIMIT greatest(0, least(sqlc.arg(page_size)::integer, 101));
