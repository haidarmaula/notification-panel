-- name: LockInstallationProject :one
SELECT status FROM applications WHERE id = sqlc.arg(application_id)::bigint FOR UPDATE;

-- name: LockInstallationUser :one
SELECT id FROM users
WHERE application_id = sqlc.arg(application_id)::bigint AND id = sqlc.arg(user_id)::bigint
  AND status = 'ACTIVE'
FOR SHARE;

-- name: CreateMobileInstallation :one
INSERT INTO mobile_installations(application_id, owner_user_id, proof_verifier)
VALUES(sqlc.arg(application_id)::bigint, sqlc.arg(owner_user_id)::bigint, sqlc.arg(proof_verifier))
RETURNING *;

-- name: LockMobileInstallation :one
SELECT * FROM mobile_installations
WHERE application_id = sqlc.arg(application_id)::bigint AND public_id = sqlc.arg(public_id)
FOR UPDATE;

-- name: LockActiveInstallationBinding :one
SELECT * FROM installation_bindings
WHERE application_id = sqlc.arg(application_id)::bigint
  AND installation_id = sqlc.arg(installation_id)::bigint AND retired_at IS NULL
FOR UPDATE;

-- name: ReserveInstallationBindingID :one
SELECT nextval('installation_bindings_id_seq')::bigint;

-- name: InsertInstallationBinding :exec
INSERT INTO installation_bindings(
    id, application_id, installation_id, owner_user_id, owner_epoch, platform,
    provider_namespace, provider_route_key, address_type, address_fingerprint,
    address_ciphertext, encryption_key_version
)
VALUES(sqlc.arg(id), sqlc.arg(application_id), sqlc.arg(installation_id),
       sqlc.arg(owner_user_id), sqlc.arg(owner_epoch), sqlc.arg(platform),
       sqlc.arg(provider_namespace), sqlc.arg(provider_route_key), sqlc.arg(address_type),
       sqlc.arg(address_fingerprint), sqlc.arg(address_ciphertext), sqlc.arg(encryption_key_version));

-- name: RetireInstallationBinding :execrows
UPDATE installation_bindings SET retired_at = now()
WHERE application_id = sqlc.arg(application_id)::bigint AND id = sqlc.arg(id)::bigint
  AND retired_at IS NULL;

-- name: AdvanceMobileInstallation :one
UPDATE mobile_installations
SET owner_user_id = sqlc.narg(owner_user_id), owner_epoch = sqlc.arg(owner_epoch),
    version = version + 1, last_seen_at = now()
WHERE application_id = sqlc.arg(application_id)::bigint AND id = sqlc.arg(id)::bigint
  AND version = sqlc.arg(expected_version)::bigint
RETURNING *;

-- name: TouchMobileInstallation :exec
UPDATE mobile_installations SET last_seen_at = now()
WHERE application_id = sqlc.arg(application_id)::bigint AND id = sqlc.arg(id)::bigint;
