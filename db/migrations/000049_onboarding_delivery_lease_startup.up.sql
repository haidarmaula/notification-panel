CREATE FUNCTION delivery_worker_scope(p_app uuid) RETURNS TABLE(generation bigint, active boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
    SELECT coalesce(CASE WHEN a.public_id=p_app THEN s.generation WHEN s.worker_role IS NOT NULL THEN -1 END,0),
        coalesce(a.public_id=p_app AND a.status='ACTIVE' AND
            setup.runtime_generation=s.generation AND setup.current_configuration_id=s.configuration_id,false)
    FROM (VALUES (session_user::regrole::oid)) AS login(role_id)
    LEFT JOIN public.application_worker_preflight_scopes s ON s.worker_role=login.role_id
    LEFT JOIN public.applications a ON a.id=s.application_id
    LEFT JOIN public.application_onboarding setup ON setup.application_id=s.application_id
$$;
REVOKE ALL ON FUNCTION delivery_worker_scope(uuid) FROM PUBLIC;
