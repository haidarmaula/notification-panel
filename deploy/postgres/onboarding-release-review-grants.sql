-- As schema owner, provision one reviewed project/environment scope for the
-- separate dispatch reviewer and executor LOGIN roles. Apply the existing
-- dispatch-policy profile first. Invoke with psql variables project_id,
-- environment, reviewer, executor. Role passwords never appear in this script.
\set ON_ERROR_STOP on
BEGIN;
SELECT public.application_dispatch_actor_valid((:'reviewer')::regrole::oid,true)
   AND public.application_dispatch_actor_valid((:'executor')::regrole::oid,false)
   AS release_roles_valid
\gset
\if :release_roles_valid
INSERT INTO public.application_onboarding_release_reviewers(project_id,environment,issuer_role,executor_role)
VALUES (:'project_id',:'environment',(:'reviewer')::regrole::oid,(:'executor')::regrole::oid);
GRANT EXECUTE ON FUNCTION public.approve_onboarding_release(uuid,text,uuid,text,uuid,uuid,bytea,timestamptz),
    public.delegate_onboarding_release(uuid,text,uuid,uuid,bigint,bytea,bytea,bytea,bytea,timestamptz) TO :"reviewer";
GRANT EXECUTE ON FUNCTION public.apply_onboarding_release(uuid,uuid,uuid) TO :"executor";
COMMIT;
\else
ROLLBACK;
DO $$ BEGIN RAISE EXCEPTION 'invalid independent dispatch reviewer/executor roles'; END $$;
\endif
