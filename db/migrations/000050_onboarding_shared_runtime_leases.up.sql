CREATE FUNCTION acknowledge_shared_application_runtime(p_component text,p_digest bytea,p_instance uuid)
RETURNS integer LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE
    scope record;
    acknowledged integer := 0;
BEGIN
    IF p_component IS NULL OR p_component NOT IN ('SERVER','ADMISSION','SCHEDULER') THEN RETURN 0; END IF;
    FOR scope IN
        SELECT app.public_id,s.generation
        FROM public.application_runtime_component_scopes s
        JOIN public.applications app ON app.id=s.application_id
        JOIN public.application_runtime_reservations r USING(application_id,generation)
        WHERE s.login_role=session_user::regrole::oid AND s.component=p_component AND s.policy_sha256=p_digest
            AND r.released_at IS NULL
        ORDER BY s.application_id,s.generation LIMIT 200
    LOOP
        IF public.acknowledge_application_runtime(scope.public_id,scope.generation,p_component,p_digest,p_instance)
        THEN acknowledged := acknowledged+1; END IF;
    END LOOP;
    RETURN acknowledged;
END;
$$;
REVOKE ALL ON FUNCTION acknowledge_shared_application_runtime(text,bytea,uuid) FROM PUBLIC;
