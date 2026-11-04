-- All mutation queries require the service's canonical control/authority locks.
-- They are persistence primitives, not an authorization boundary.

-- name: InsertReplayRequest :execrows
INSERT INTO notification_replay_requests(application_id,id,staff_user_id,operational_grant_id,selection_hash,change_reference)
VALUES(sqlc.arg(application_id),sqlc.arg(id),sqlc.arg(staff_user_id),sqlc.arg(operational_grant_id),sqlc.arg(selection_hash),sqlc.arg(change_reference))
ON CONFLICT(application_id,id) DO NOTHING;

-- name: LockReplayRequest :one
SELECT * FROM notification_replay_requests
WHERE application_id=sqlc.arg(application_id)::bigint AND id=sqlc.arg(id)::uuid FOR UPDATE;

-- name: InsertReplayControl :exec
INSERT INTO notification_replay_controls(log_source_id,original_partition,original_offset)
VALUES(sqlc.arg(log_source_id),sqlc.arg(original_partition),sqlc.arg(original_offset))
ON CONFLICT(log_source_id,original_partition,original_offset) DO NOTHING;

-- name: LockReplayControl :one
SELECT * FROM notification_replay_controls WHERE log_source_id=sqlc.arg(log_source_id)::uuid
    AND original_partition=sqlc.arg(original_partition)::integer AND original_offset=sqlc.arg(original_offset)::bigint FOR UPDATE;

-- name: LockReplayEvidence :one
SELECT * FROM notification_send_dead_letters WHERE log_source_id=sqlc.arg(log_source_id)::uuid
    AND original_partition=sqlc.arg(original_partition)::integer AND original_offset=sqlc.arg(original_offset)::bigint FOR UPDATE;

-- name: InsertReplayAttempt :one
INSERT INTO notification_replay_attempts(application_id,request_id,log_source_id,original_partition,original_offset,
    notification_id,origin_epoch,source_epoch,claim_token,lease_expires_at,outcome,finished_at)
VALUES(sqlc.arg(application_id),sqlc.arg(request_id),sqlc.arg(log_source_id),sqlc.arg(original_partition),sqlc.arg(original_offset),
    sqlc.narg(notification_id),sqlc.narg(origin_epoch),sqlc.narg(source_epoch),sqlc.narg(claim_token),
    CASE WHEN sqlc.narg(claim_token)::uuid IS NOT NULL THEN clock_timestamp()+interval '30 seconds' ELSE NULL END,
    sqlc.arg(outcome),CASE WHEN sqlc.arg(outcome)::text='started' THEN NULL ELSE clock_timestamp() END)
RETURNING *;

-- name: GetReplayAttempt :one
SELECT * FROM notification_replay_attempts WHERE application_id=sqlc.arg(application_id)::bigint AND request_id=sqlc.arg(request_id)::uuid
    AND log_source_id=sqlc.arg(log_source_id)::uuid AND original_partition=sqlc.arg(original_partition)::integer
    AND original_offset=sqlc.arg(original_offset)::bigint;

-- name: ClaimReplayControl :execrows
UPDATE notification_replay_controls SET active_application_id=sqlc.arg(application_id),active_request_id=sqlc.arg(request_id),
    active_claim_token=sqlc.arg(claim_token)
WHERE log_source_id=sqlc.arg(log_source_id)::uuid AND original_partition=sqlc.arg(original_partition)::integer
    AND original_offset=sqlc.arg(original_offset)::bigint AND active_claim_token IS NULL AND resolved_outcome IS NULL
    AND EXISTS(SELECT 1 FROM notification_replay_attempts a
        WHERE a.application_id=sqlc.arg(application_id)::bigint AND a.request_id=sqlc.arg(request_id)::uuid
        AND a.log_source_id=sqlc.arg(log_source_id)::uuid AND a.original_partition=sqlc.arg(original_partition)::integer
        AND a.original_offset=sqlc.arg(original_offset)::bigint AND a.claim_token=sqlc.arg(claim_token)::uuid
        AND a.outcome='started' AND a.lease_expires_at>=clock_timestamp()+interval '5 seconds');

-- name: FinishReplayAttempt :execrows
UPDATE notification_replay_attempts SET outcome=sqlc.arg(outcome),finished_at=clock_timestamp()
WHERE application_id=sqlc.arg(application_id)::bigint AND request_id=sqlc.arg(request_id)::uuid
    AND log_source_id=sqlc.arg(log_source_id)::uuid AND original_partition=sqlc.arg(original_partition)::integer
    AND original_offset=sqlc.arg(original_offset)::bigint AND claim_token=sqlc.arg(claim_token)::uuid
    AND outcome='started' AND lease_expires_at>clock_timestamp()
    AND sqlc.arg(outcome)::text IN ('held','rejected','expired','reconciliation_required','already_prepared','terminal','published','publish_unconfirmed')
    AND EXISTS(SELECT 1 FROM notification_replay_controls c
        WHERE c.log_source_id=sqlc.arg(log_source_id)::uuid AND c.original_partition=sqlc.arg(original_partition)::integer
        AND c.original_offset=sqlc.arg(original_offset)::bigint AND c.active_application_id=sqlc.arg(application_id)::bigint
        AND c.active_request_id=sqlc.arg(request_id)::uuid AND c.active_claim_token=sqlc.arg(claim_token)::uuid);

-- name: AbandonExpiredReplayAttempt :execrows
UPDATE notification_replay_attempts SET outcome='abandoned_unknown',finished_at=clock_timestamp()
WHERE application_id=sqlc.arg(application_id)::bigint AND request_id=sqlc.arg(request_id)::uuid
    AND log_source_id=sqlc.arg(log_source_id)::uuid AND original_partition=sqlc.arg(original_partition)::integer
    AND original_offset=sqlc.arg(original_offset)::bigint AND claim_token=sqlc.arg(claim_token)::uuid
    AND outcome='started' AND lease_expires_at<=clock_timestamp()
    AND EXISTS(SELECT 1 FROM notification_replay_controls c
        WHERE c.log_source_id=sqlc.arg(log_source_id)::uuid AND c.original_partition=sqlc.arg(original_partition)::integer
        AND c.original_offset=sqlc.arg(original_offset)::bigint AND c.active_application_id=sqlc.arg(application_id)::bigint
        AND c.active_request_id=sqlc.arg(request_id)::uuid AND c.active_claim_token=sqlc.arg(claim_token)::uuid);

-- name: ClearFinishedReplayClaim :execrows
UPDATE notification_replay_controls SET active_application_id=NULL,active_request_id=NULL,active_claim_token=NULL
WHERE log_source_id=sqlc.arg(log_source_id)::uuid AND original_partition=sqlc.arg(original_partition)::integer
    AND original_offset=sqlc.arg(original_offset)::bigint AND active_application_id=sqlc.arg(application_id)::bigint
    AND active_request_id=sqlc.arg(request_id)::uuid AND active_claim_token=sqlc.arg(claim_token)::uuid
    AND EXISTS(SELECT 1 FROM notification_replay_attempts a
        WHERE a.application_id=sqlc.arg(application_id)::bigint AND a.request_id=sqlc.arg(request_id)::uuid
        AND a.log_source_id=sqlc.arg(log_source_id)::uuid AND a.original_partition=sqlc.arg(original_partition)::integer
        AND a.original_offset=sqlc.arg(original_offset)::bigint AND a.claim_token=sqlc.arg(claim_token)::uuid
        AND a.outcome IN ('held','rejected','expired','reconciliation_required','publish_unconfirmed','abandoned_unknown'));

-- name: ResolveReplayControl :execrows
UPDATE notification_replay_controls SET active_application_id=NULL,active_request_id=NULL,active_claim_token=NULL,
    resolved_application_id=sqlc.arg(application_id),resolved_request_id=sqlc.arg(request_id),resolved_outcome=sqlc.arg(outcome)
WHERE log_source_id=sqlc.arg(log_source_id)::uuid AND original_partition=sqlc.arg(original_partition)::integer
    AND original_offset=sqlc.arg(original_offset)::bigint AND resolved_outcome IS NULL
    AND ((active_claim_token IS NULL AND sqlc.narg(expected_token)::uuid IS NULL)
        OR (active_claim_token=sqlc.narg(expected_token)::uuid AND active_application_id=sqlc.arg(application_id)::bigint
            AND active_request_id=sqlc.arg(request_id)::uuid))
    AND sqlc.arg(outcome)::text IN ('published','already_prepared','terminal')
    AND (sqlc.arg(outcome)::text<>'published' OR sqlc.narg(expected_token)::uuid IS NOT NULL);

-- name: ListExpiredReplayClaims :many
SELECT a.application_id,a.request_id,a.log_source_id,a.original_partition,a.original_offset,a.claim_token
FROM notification_replay_attempts a JOIN notification_replay_controls c
    ON c.log_source_id=a.log_source_id AND c.original_partition=a.original_partition AND c.original_offset=a.original_offset
    AND c.active_application_id=a.application_id AND c.active_request_id=a.request_id AND c.active_claim_token=a.claim_token
WHERE a.outcome='started' AND a.lease_expires_at<=clock_timestamp()
ORDER BY a.lease_expires_at,a.log_source_id,a.original_partition,a.original_offset
LIMIT sqlc.arg(page_size)::integer;
