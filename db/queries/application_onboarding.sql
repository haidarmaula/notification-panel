-- name: GetApplicationOnboarding :one
SELECT o.*,a.public_id,a.status AS application_status,a.revision AS application_revision,
    COALESCE(p.dispatch_mode,'HELD')::text AS dispatch_mode,
    COALESCE(p.dispatch_revision,0)::bigint AS dispatch_revision
FROM public.application_onboarding o JOIN public.applications a ON a.id=o.application_id
LEFT JOIN public.application_dispatch_policy p ON p.application_id=o.application_id
WHERE a.public_id=$1;

-- name: LockApplicationOnboarding :one
SELECT * FROM public.application_onboarding WHERE application_id=$1 FOR UPDATE;

-- name: ConfigureApplicationEnrollment :one
UPDATE public.application_onboarding SET enrollment_profile_id=$2,enrollment_platforms=$3,
    revision=revision+1,updated_at=clock_timestamp()
WHERE application_id=$1 AND revision=$4 AND current_configuration_id IS NULL
RETURNING *;

-- name: ReserveApplicationFCMIngestion :one
INSERT INTO public.application_onboarding_ingestions(id,application_id,actor_id,session_id,session_epoch,auth_epoch,
    expected_revision,idempotency_key,request_fingerprint,fingerprint_key_version,configuration_id,environment,target_project_id,service_account_email,expires_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,clock_timestamp()+interval '24 hours')
RETURNING *;

-- name: FindApplicationFCMIngestion :one
SELECT * FROM public.application_onboarding_ingestions WHERE application_id=$1 AND actor_id=$2 AND idempotency_key=$3;

-- name: GetApplicationFCMIngestion :one
SELECT * FROM public.application_onboarding_ingestions WHERE application_id=$1 AND id=$2;

-- name: CompleteApplicationFCMIngestion :execrows
UPDATE public.application_onboarding_ingestions SET state='COMMITTED'
WHERE application_id=$1 AND id=$2 AND state='RESERVED' AND expires_at>clock_timestamp();

-- name: CreateApplicationProviderConfiguration :one
INSERT INTO public.application_provider_configurations(id,application_id,version,environment,target_project_id,
    service_account_email,file_reference,created_by)
SELECT sqlc.arg(id)::uuid,sqlc.arg(application_id)::bigint,COALESCE(MAX(version),0)+1,
    sqlc.arg(environment)::text,sqlc.arg(target_project_id)::text,sqlc.arg(service_account_email)::text,
    sqlc.arg(file_reference)::uuid,sqlc.arg(created_by)::bigint
FROM public.application_provider_configurations WHERE application_id=sqlc.arg(application_id)::bigint
RETURNING *;

-- name: SetApplicationOnboardingCandidate :one
UPDATE public.application_onboarding SET environment=$2,candidate_configuration_id=$3,
    revision=revision+1,updated_at=clock_timestamp()
WHERE application_id=$1 AND revision=$4 AND (environment IS NULL OR environment=$2)
RETURNING *;

-- name: GetApplicationProviderConfiguration :one
SELECT * FROM public.application_provider_configurations WHERE application_id=$1 AND id=$2;

-- name: ListApplicationProviderChecks :many
SELECT DISTINCT ON (kind) * FROM public.application_provider_checks
WHERE application_id=$1 AND configuration_id=$2 ORDER BY kind,checked_at DESC,id DESC;

-- name: GetApplicationOnboardingOperation :one
SELECT * FROM public.application_onboarding_operations WHERE application_id=$1 AND id=$2;

-- name: GetLastApplicationOnboardingOperation :one
SELECT * FROM public.application_onboarding_operations WHERE application_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1;

-- name: FindApplicationOnboardingOperation :one
SELECT * FROM public.application_onboarding_operations WHERE application_id=$1 AND actor_id=$2 AND kind=$3 AND idempotency_key=$4;

-- name: CreateApplicationOnboardingOperation :one
INSERT INTO public.application_onboarding_operations(id,application_id,kind,actor_id,session_id,session_epoch,auth_epoch,
    idempotency_key,request_fingerprint,fingerprint_key_version,configuration_id,approval_id,cohort_id,test_installation_id,
    expected_setup_revision,expected_application_revision,expected_dispatch_revision,origin_epoch,source_epoch,installation_ids,reason)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
RETURNING *;

-- name: AdvanceApplicationOnboardingRevision :one
UPDATE public.application_onboarding SET revision=revision+1,updated_at=clock_timestamp()
WHERE application_id=$1 AND revision=$2 RETURNING revision;

-- name: ListApplicationOnboardingApprovals :many
SELECT * FROM public.application_onboarding_approvals WHERE application_id=$1 ORDER BY id LIMIT 101;

-- name: FindApplicationOnboardingRequest :one
SELECT * FROM public.application_onboarding_requests WHERE application_id=$1 AND actor_id=$2 AND kind=$3 AND idempotency_key=$4;

-- name: SaveApplicationOnboardingRequest :exec
INSERT INTO public.application_onboarding_requests(application_id,actor_id,kind,idempotency_key,request_fingerprint,fingerprint_key_version,response_data)
VALUES($1,$2,$3,$4,$5,$6,$7);

-- name: GetOnboardingActorEpoch :one
SELECT auth_epoch FROM public.staff_users WHERE id=$1 AND is_active;

-- name: HoldApplicationOnboardingDispatch :execrows
UPDATE public.application_dispatch_policy SET hold_new=true,coverage_approved=false
WHERE application_id=$1;

-- name: HasApplicationOnboardingMutation :one
SELECT EXISTS(SELECT FROM public.application_onboarding_operations WHERE application_id=$1 AND kind<>'VERIFY' AND state IN ('PENDING','RUNNING'));

-- name: SupersedeUnusableOnboardingRelease :exec
SELECT public.supersede_unusable_onboarding_release(sqlc.arg(application_id)::bigint,sqlc.arg(kind)::text);

-- name: GetApplicationOnboardingApproval :one
SELECT * FROM public.application_onboarding_approvals WHERE application_id=$1 AND id=$2;

-- name: SupersedeApplicationOnboardingMutations :exec
UPDATE public.application_onboarding_operations SET state='SUPERSEDED',completed_at=clock_timestamp(),failure_code='DISPATCH_HELD',
    lease_owner=NULL,lease_expires_at=NULL
WHERE application_id=$1 AND kind<>'VERIFY' AND state IN ('PENDING','RUNNING');

-- name: CountRecentApplicationFCMVerifications :one
SELECT count(*) FROM public.application_onboarding_operations WHERE application_id=$1 AND kind='VERIFY' AND created_at>clock_timestamp()-interval '1 minute';

-- name: VerifyApplicationFCMReservation :one
SELECT EXISTS(
    SELECT FROM public.application_onboarding_ingestions i
    JOIN public.applications a ON a.id=i.application_id
    JOIN public.application_onboarding o ON o.application_id=a.id
    JOIN public.staff_users s ON s.id=i.actor_id AND s.is_active AND s.auth_epoch=i.auth_epoch
    JOIN public.roles r ON r.id=s.role_id AND r.name='SUPER_ADMIN'
    JOIN public.staff_sessions f ON f.id=i.session_id AND f.staff_user_id=s.id AND f.auth_epoch=s.auth_epoch
    JOIN public.staff_session_control control ON control.singleton AND control.session_epoch=i.session_epoch
    WHERE a.public_id=sqlc.arg(public_id)::uuid AND i.id=sqlc.arg(reservation_id)::uuid
      AND i.configuration_id=sqlc.arg(configuration_id)::uuid AND i.environment=sqlc.arg(environment)::text
      AND i.target_project_id=sqlc.arg(target_project_id)::text AND i.service_account_email=sqlc.arg(service_account_email)::text
      AND i.state='RESERVED' AND i.expires_at>clock_timestamp() AND a.status IN ('PROVISIONING','ACTIVE')
      AND i.expected_revision=o.revision AND f.session_epoch=control.session_epoch AND f.revoked_at IS NULL
      AND f.expires_at>clock_timestamp()
);

-- name: ClaimApplicationOnboardingVerification :one
SELECT public.claim_application_onboarding_verification(sqlc.arg(public_id)::uuid,sqlc.arg(lease_owner)::uuid)::uuid AS operation_id;

-- name: FinishApplicationOnboardingVerification :one
SELECT public.finish_application_onboarding_verification(sqlc.arg(operation_id)::uuid,sqlc.arg(lease_owner)::uuid,
    sqlc.arg(lease_token)::bigint,sqlc.arg(outcome)::text,sqlc.narg(failure_code)::text)::boolean AS applied;

-- name: HasApplicationOnboardingVerifier :one
SELECT EXISTS(SELECT FROM public.application_onboarding_verifier_scopes WHERE application_id=$1);

-- name: FindOnboardingTestInstallation :one
SELECT id FROM public.mobile_installations WHERE application_id=$1 AND public_id=$2;
