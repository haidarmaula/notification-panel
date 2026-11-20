-- Bind the operation returned by HTTP under the reviewed issuer login.
SELECT runtime_generation AS release_generation FROM application_onboarding WHERE application_id=:app_id \gset
SET SESSION AUTHORIZATION fixture_dispatch_operator;
\set ON_ERROR_ROLLBACK on
\set ON_ERROR_STOP off
SELECT public.bind_onboarding_release(:'release_operation',:'release_grant',:'release_generation');
\set bind_denied :SQLSTATE
\set ON_ERROR_STOP on
\set ON_ERROR_ROLLBACK off
RESET SESSION AUTHORIZATION;
SELECT 1/(:'bind_denied'='42501')::int AS binder_acl_ok \gset
SET SESSION AUTHORIZATION fixture_dispatch_issuer;
\set ON_ERROR_ROLLBACK on
\set ON_ERROR_STOP off
SELECT public.bind_onboarding_release(:'release_operation',:'release_grant',:'release_generation'::bigint+1);
\set bind_wrong_generation :SQLSTATE
\set ON_ERROR_STOP on
\set ON_ERROR_ROLLBACK off
SELECT 1/(:'bind_wrong_generation'='55000')::int AS binder_generation_ok \gset
SELECT 1/(public.bind_onboarding_release(:'release_operation',:'release_grant',:'release_generation')=:'release_operation'::uuid)::int AS bind_ok \gset
SELECT 1/(public.bind_onboarding_release(:'release_operation',:'release_grant',:'release_generation')=:'release_operation'::uuid)::int AS bind_replay_ok \gset
RESET SESSION AUTHORIZATION;
