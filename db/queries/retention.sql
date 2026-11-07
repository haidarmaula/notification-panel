-- name: RunRetentionBatch :one
SELECT b.outcome::text AS outcome,b.quantity::integer AS quantity
FROM public.run_retention_batch(
    sqlc.arg(grant_id)::uuid,sqlc.arg(application_public_id)::uuid,
    sqlc.arg(origin_epoch)::uuid,sqlc.arg(source_epoch)::uuid,
    sqlc.arg(record_class)::text,sqlc.arg(batch_no)::integer,sqlc.arg(apply)::boolean
) AS b;
