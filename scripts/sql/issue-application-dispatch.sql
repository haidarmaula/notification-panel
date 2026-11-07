-- Connect using the distinct dispatch reviewer login; each value is bound as data.
\set ON_ERROR_STOP on
INSERT INTO public.application_dispatch_grants
    (id,project_id,operator_role,application_revision,expected_revision,previous_mode,destination,cohort_id,origin_epoch,source_epoch,reason,
     legacy_evidence,consent_evidence,coverage_evidence,quota_evidence,expires_at)
VALUES
    (:'grant_id'::uuid,:'project'::uuid,:'executor'::regrole::oid,:'application_revision'::bigint,:'dispatch_revision'::bigint,
     :'from_mode',:'destination',NULLIF(:'cohort','')::uuid,:'origin'::uuid,:'source'::uuid,:'reason',
     decode(:'legacy_sha256','hex'),decode(:'consent_sha256','hex'),decode(:'coverage_sha256','hex'),decode(:'quota_sha256','hex'),:'expires_utc'::timestamptz);
