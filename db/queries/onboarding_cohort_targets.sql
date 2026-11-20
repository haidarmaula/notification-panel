-- name: InitializeOnboardingCohort :one
WITH initialized AS MATERIALIZED (
    SELECT public.initialize_onboarding_cohort(sqlc.arg(project)::uuid,sqlc.arg(environment)::text,
        sqlc.arg(operation)::uuid,sqlc.arg(owner)::uuid)::bigint AS lease_token
)
SELECT lease_token FROM initialized WHERE lease_token IS NOT NULL;

-- name: FinalizeOnboardingCohort :one
SELECT public.finalize_onboarding_cohort(sqlc.arg(project)::uuid,sqlc.arg(environment)::text,
    sqlc.arg(operation)::uuid,sqlc.arg(owner)::uuid,sqlc.arg(lease_token)::bigint)::boolean AS applied;

-- name: ClaimOnboardingCohortTarget :one
SELECT c.application_key::bigint AS application_key,c.application::uuid AS application,e.environment,
    c.operation_id::uuid AS operation_id,c.cohort_id::uuid AS cohort_id,c.approval_id::uuid AS approval_id,
    c.configuration_id::uuid AS configuration_id,c.installation_key::bigint AS installation_key,
    c.installation_id::uuid AS installation_id,c.binding_epoch::bigint AS binding_epoch,c.file_reference::uuid AS file_reference,
    c.target_project_id::text AS target_project_id,c.service_account_email::text AS service_account_email,
    c.address_type::text AS address_type,c.address_ciphertext::bytea AS address_ciphertext,
    c.encryption_key_version::integer AS encryption_key_version,c.lease_token::bigint AS lease_token,c.target_owner::uuid AS target_owner
FROM public.claim_onboarding_cohort_target(sqlc.arg(project)::uuid,sqlc.arg(environment)::text,sqlc.arg(owner)::uuid) AS c
-- Keep the nullable text mapping of the onboarding setup environment in sqlc.
CROSS JOIN LATERAL (SELECT NULL::text AS environment WHERE false UNION ALL SELECT c.environment) e;

-- name: FinishOnboardingCohortTarget :execrows
SELECT 1 WHERE public.finish_onboarding_cohort_target(sqlc.arg(project)::uuid,sqlc.arg(environment)::text,
    sqlc.arg(operation)::uuid,sqlc.arg(cohort)::uuid,sqlc.arg(approval)::uuid,sqlc.arg(configuration)::uuid,
    sqlc.arg(installation)::uuid,sqlc.arg(binding_epoch)::bigint,sqlc.arg(owner)::uuid,sqlc.arg(lease_token)::bigint,
    sqlc.arg(outcome)::text,sqlc.arg(failure_code)::text);
