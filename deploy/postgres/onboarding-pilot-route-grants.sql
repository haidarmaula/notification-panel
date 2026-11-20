-- Run as trusted owner for one application and environment. The issuer and
-- executor are distinct LOGINs with no raw M37 insert or execute privileges.
-- psql variables: project_id, environment, reviewer, executor.
\set ON_ERROR_STOP on
BEGIN;
SELECT public.onboarding_route_actor_valid((:'reviewer')::regrole::oid)
   AND public.onboarding_route_actor_valid((:'executor')::regrole::oid) AS route_roles_valid
\gset
\if :route_roles_valid
INSERT INTO public.application_onboarding_route_reviewers(project_id,environment,issuer_role,executor_role)
VALUES (:'project_id',:'environment',(:'reviewer')::regrole::oid,(:'executor')::regrole::oid);
GRANT EXECUTE ON FUNCTION public.approve_onboarding_pilot_route(uuid,text,uuid,uuid,uuid,timestamptz) TO :"reviewer";
GRANT EXECUTE ON FUNCTION public.apply_onboarding_pilot_route(uuid,text,uuid) TO :"executor";
COMMIT;
\else
ROLLBACK;
DO $$ BEGIN RAISE EXCEPTION 'invalid independent pilot route reviewer/executor roles'; END $$;
\endif
