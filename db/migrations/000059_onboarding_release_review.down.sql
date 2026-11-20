CREATE OR REPLACE FUNCTION supersede_unusable_onboarding_release(p_application bigint,p_kind text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE
    op public.application_onboarding_operations%ROWTYPE;
    g public.application_dispatch_grants%ROWTYPE;
    project_name text;
BEGIN
    IF p_kind IS NULL OR p_kind NOT IN ('RELEASE_PILOT','RELEASE_FULL') THEN RETURN false; END IF;
    SELECT name INTO project_name FROM public.applications WHERE id=p_application FOR NO KEY UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    SELECT * INTO op FROM public.application_onboarding_operations
    WHERE application_id=p_application AND kind=p_kind AND state='PENDING' AND lease_owner IS NULL;
    IF NOT FOUND THEN RETURN false; END IF;
    SELECT grant_row.* INTO g FROM public.application_onboarding_dispatch_delegations d
    JOIN public.application_dispatch_grants grant_row ON grant_row.id=d.grant_id
    WHERE d.operation_id=op.id FOR UPDATE OF grant_row;
    IF NOT FOUND OR g.applied_at IS NOT NULL OR (g.revoked_at IS NULL AND g.expires_at>clock_timestamp())
    THEN RETURN false; END IF;

    UPDATE public.application_onboarding_operations SET state='SUPERSEDED',completed_at=clock_timestamp(),
        failure_code='RELEASE_GRANT_UNAVAILABLE' WHERE id=op.id AND state='PENDING' AND lease_owner IS NULL;
    IF NOT FOUND THEN RETURN false; END IF;
    INSERT INTO public.audit_logs(application_id,actor_user_id,action,entity_type,entity_id,entity_name,after_json)
    VALUES(p_application,op.actor_id,'APPLICATION_ONBOARDING_OPERATION_COMPLETE','application_onboarding',p_application,project_name,
        jsonb_build_object('operation_id',op.id,'kind',op.kind,'outcome','SUPERSEDED','failure_code','RELEASE_GRANT_UNAVAILABLE'));
    RETURN true;
END;
$$;
REVOKE ALL ON FUNCTION supersede_unusable_onboarding_release(bigint,text) FROM PUBLIC;

DROP FUNCTION public.delegate_onboarding_release(uuid,text,uuid,uuid,bigint,bytea,bytea,bytea,bytea,timestamptz);
DROP FUNCTION public.approve_onboarding_release(uuid,text,uuid,text,uuid,uuid,bytea,timestamptz);
DROP TABLE public.application_onboarding_release_reviewers;
