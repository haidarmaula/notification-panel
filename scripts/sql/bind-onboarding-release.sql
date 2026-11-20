-- Run with the reviewed grant's issuer login, not the release executor.
-- Set operation_id, grant_id and runtime_generation with psql -v.
\set ON_ERROR_STOP on
SELECT public.bind_onboarding_release(:'operation_id'::uuid, :'grant_id'::uuid, :'runtime_generation'::bigint) AS operation_id;
