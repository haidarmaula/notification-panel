-- Connect using the exact designated executor login.
\set ON_ERROR_STOP on
SELECT public.apply_application_dispatch(:'grant_id'::uuid,:'project'::uuid) AS applied_revision;
