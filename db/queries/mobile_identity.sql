-- name: ResolveMobileIdentity :one
SELECT u.id
FROM mobile_identities m
JOIN users u ON u.application_id = m.application_id AND u.id = m.user_id
WHERE m.application_id = sqlc.arg(application_id)::bigint
  AND m.identity_namespace = sqlc.arg(identity_namespace)
  AND m.subject = sqlc.arg(subject)
  AND u.status = 'ACTIVE';
