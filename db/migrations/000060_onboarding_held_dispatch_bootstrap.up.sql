CREATE TABLE public.application_onboarding_held_dispatch_bootstraps (
    application_id bigint PRIMARY KEY REFERENCES public.application_onboarding(application_id),
    runtime_executor oid NOT NULL,
    profile_revision bigint NOT NULL CHECK (profile_revision>0),
    push_ttl_seconds integer NOT NULL CHECK (push_ttl_seconds BETWEEN 1 AND 604800),
    key_ttl_seconds integer NOT NULL CHECK (key_ttl_seconds BETWEEN 1 AND 604800),
    bootstrapped_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
REVOKE ALL ON public.application_onboarding_held_dispatch_bootstraps FROM PUBLIC;
CREATE TRIGGER protect_onboarding_held_dispatch_bootstrap BEFORE INSERT OR UPDATE OR DELETE
    ON public.application_onboarding_held_dispatch_bootstraps
    FOR EACH ROW EXECUTE FUNCTION public.protect_application_worker_preflight_scope();
CREATE TRIGGER protect_onboarding_held_dispatch_bootstrap_history BEFORE TRUNCATE
    ON public.application_onboarding_held_dispatch_bootstraps
    FOR EACH STATEMENT EXECUTE FUNCTION public.protect_application_worker_preflight_scope();

-- Bootstrap only the initial held policy. The restricted preparation LOGIN
-- supplies explicit deployment TTLs for its owner-assigned application/profile.
CREATE FUNCTION public.bootstrap_onboarding_held_dispatch(
    p_project uuid, p_environment text, p_profile_revision bigint, p_push_ttl integer, p_key_ttl integer
) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE
    app public.applications%ROWTYPE;
    setup public.application_onboarding%ROWTYPE;
    policy public.application_dispatch_policy%ROWTYPE;
    runtime public.notification_runtime%ROWTYPE;
BEGIN
    IF current_setting('transaction_isolation')<>'read committed'
        OR pg_has_role(session_user,current_user,'MEMBER')
        OR pg_has_role(session_user,to_regrole('hello_onboarding_runtime_executor'),'USAGE') IS DISTINCT FROM true
        OR NOT EXISTS (SELECT FROM pg_roles WHERE oid=session_user::regrole::oid AND rolcanlogin AND NOT rolsuper
            AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolreplication AND NOT rolbypassrls)
        OR NOT EXISTS (SELECT FROM public.application_runtime_executor_scopes s JOIN public.applications a ON a.id=s.application_id
            WHERE s.role_id=session_user::regrole::oid AND a.public_id=p_project
              AND (s.environment,s.profile_revision)=(p_environment,p_profile_revision))
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='dispatch policy scope unavailable'; END IF;
    IF p_push_ttl IS NULL OR p_push_ttl NOT BETWEEN 1 AND 604800 OR p_key_ttl IS NULL OR p_key_ttl NOT BETWEEN 1 AND 604800
    THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid dispatch policy TTL'; END IF;

    SELECT * INTO STRICT runtime FROM public.notification_runtime WHERE singleton FOR SHARE;
    SELECT * INTO STRICT app FROM public.applications WHERE public_id=p_project FOR NO KEY UPDATE;
    SELECT * INTO STRICT setup FROM public.application_onboarding WHERE application_id=app.id FOR UPDATE;
    SELECT * INTO policy FROM public.application_dispatch_policy WHERE application_id=app.id FOR UPDATE;
    IF FOUND THEN
        IF EXISTS (SELECT FROM public.application_onboarding_held_dispatch_bootstraps b
            WHERE b.application_id=app.id AND b.runtime_executor=session_user::regrole::oid
                AND (b.profile_revision,b.push_ttl_seconds,b.key_ttl_seconds)=(p_profile_revision,p_push_ttl,p_key_ttl))
        THEN RETURN 1; END IF;
        RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='dispatch policy already initialized';
    END IF;
    IF runtime.sealed OR runtime.migration_hold OR app.status<>'PROVISIONING' OR NOT setup.greenfield
        OR setup.current_configuration_id IS NOT NULL OR setup.runtime_generation<>0 OR setup.environment IS DISTINCT FROM p_environment
    THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='dispatch policy bootstrap prerequisites unavailable'; END IF;

    INSERT INTO public.application_dispatch_policy(application_id,hold_new,coverage_approved,push_ttl_seconds,key_ttl_seconds)
    VALUES(app.id,true,false,p_push_ttl,p_key_ttl);
    UPDATE public.application_onboarding SET revision=revision+1,updated_at=clock_timestamp() WHERE application_id=app.id;
    INSERT INTO public.application_onboarding_held_dispatch_bootstraps(
        application_id,runtime_executor,profile_revision,push_ttl_seconds,key_ttl_seconds)
    VALUES(app.id,session_user::regrole::oid,p_profile_revision,p_push_ttl,p_key_ttl);
    RETURN 1;
END;
$$;
REVOKE ALL ON FUNCTION public.bootstrap_onboarding_held_dispatch(uuid,text,bigint,integer,integer) FROM PUBLIC;
