-- name: ConsumeMobileOrigin :execrows
INSERT INTO mobile_origin_uses (application_public_id, issuer, nonce_digest, expires_at)
SELECT sqlc.arg(application_public_id), sqlc.arg(issuer), sqlc.arg(nonce_digest), sqlc.arg(expires_at)::timestamptz
WHERE sqlc.arg(expires_at)::timestamptz > clock_timestamp()
ON CONFLICT (application_public_id, issuer, nonce_digest) DO NOTHING;
