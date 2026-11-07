-- The exact executor login must connect directly. A committed retry returns
-- the granted transition's revision, which may precede the current revision.
\set ON_ERROR_STOP on
SELECT public.apply_application_lifecycle(:'grant_id'::uuid, :'project'::uuid) AS applied_revision;
