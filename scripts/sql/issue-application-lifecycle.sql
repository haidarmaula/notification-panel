-- Connect using the independently provisioned reviewer login. psql quotes
-- each supplied value as data. Preserve the same grant UUID for the review.
\set ON_ERROR_STOP on
INSERT INTO public.application_lifecycle_grants
    (id, project_id, operator_role, origin_epoch, source_epoch, expected_revision, previous_status, destination,
     ownership_evidence, writer_fence_evidence, cohort_fence_evidence, retired_writer_roles, expires_at)
VALUES
    (:'grant_id'::uuid, :'project'::uuid, :'executor'::regrole::oid, :'origin'::uuid, :'source'::uuid,
     :'revision'::bigint, :'from_status', :'destination', decode(:'ownership_sha256','hex'),
     decode(:'writers_sha256','hex'), decode(:'cohorts_sha256','hex'), :'retired_roles'::name[], :'expires_utc'::timestamptz);
