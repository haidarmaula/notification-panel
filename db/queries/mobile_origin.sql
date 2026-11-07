-- name: ConsumeMobileOrigin :execrows
WITH scope AS MATERIALIZED (
    SELECT public_id FROM applications WHERE public_id=sqlc.arg(application_public_id)::uuid FOR SHARE
)
INSERT INTO mobile_origin_uses (application_public_id, issuer, nonce_digest, expires_at)
SELECT scope.public_id, sqlc.arg(issuer), sqlc.arg(nonce_digest), sqlc.arg(expires_at)::timestamptz FROM scope
WHERE sqlc.arg(expires_at)::timestamptz > clock_timestamp()
ON CONFLICT (application_public_id, issuer, nonce_digest) DO NOTHING;
