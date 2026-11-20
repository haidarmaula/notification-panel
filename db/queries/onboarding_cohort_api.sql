-- name: ListOnboardingTestInstallations :many
SELECT i.public_id AS installation_id, b.platform, i.last_seen_at
FROM public.mobile_installations i
JOIN public.installation_bindings b ON (b.application_id,b.installation_id,b.owner_user_id,b.owner_epoch)=(i.application_id,i.id,i.owner_user_id,i.owner_epoch)
JOIN public.users u ON (u.application_id,u.id)=(i.application_id,i.owner_user_id)
JOIN public.application_onboarding s ON s.application_id=i.application_id
WHERE i.application_id=sqlc.arg(application_id)
  AND i.public_id>sqlc.arg(cursor_id)::uuid
  AND b.retired_at IS NULL AND u.status='ACTIVE'
  AND s.enrollment_platforms @> jsonb_build_array(jsonb_build_object('platform',b.platform))
ORDER BY i.public_id
LIMIT 101;

-- name: ListOnboardingCohortTargetStatus :many
SELECT installation_id, platform, outcome, failure_code, checked_at
FROM public.application_onboarding_cohort_target_status
WHERE application_id=sqlc.arg(application_id) AND operation_id=sqlc.arg(operation_id)
ORDER BY installation_id
LIMIT 10;
