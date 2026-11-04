-- name: LockSendLogSource :one
SELECT * FROM notification_log_sources WHERE id=sqlc.arg(id)::uuid FOR SHARE;

-- name: InsertSendQuarantine :execrows
INSERT INTO notification_send_dead_letters(log_source_id,original_partition,original_offset,original_key,original_payload,reason,attempts)
VALUES(sqlc.arg(log_source_id),sqlc.arg(original_partition),sqlc.arg(original_offset),sqlc.arg(original_key),sqlc.arg(original_payload),sqlc.arg(reason),sqlc.arg(attempts))
ON CONFLICT(log_source_id,original_partition,original_offset) DO NOTHING;

-- name: GetSendQuarantine :one
SELECT * FROM notification_send_dead_letters WHERE log_source_id=sqlc.arg(log_source_id)::uuid
  AND original_partition=sqlc.arg(original_partition)::integer AND original_offset=sqlc.arg(original_offset)::bigint;
