CREATE SCHEMA hello_delivery_internal;
REVOKE ALL ON SCHEMA hello_delivery_internal FROM PUBLIC;

CREATE FUNCTION hello_delivery_internal.scoped_application() RETURNS bigint
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
    SELECT application_id FROM public.application_worker_preflight_scopes
    WHERE worker_role = session_user::regrole::oid
$$;
GRANT EXECUTE ON FUNCTION hello_delivery_internal.scoped_application() TO PUBLIC;

-- The restriction follows the LOGIN even after SET ROLE, including in owner-defined routines and views.
DO $$
DECLARE entry record;
BEGIN
    FOR entry IN SELECT * FROM (VALUES
        ('applications','id'),
        ('application_dispatch_policy','application_id'),
        ('notifications','application_id'),
        ('notification_preparations','application_id'),
        ('users','application_id'),
        ('notification_delivery_intents','application_id'),
        ('mobile_installations','application_id'),
        ('installation_bindings','application_id'),
        ('notification_receipt_metadata','application_id'),
        ('notification_send_outbox','application_id'),
        ('notification_update_outbox','application_id'),
        ('notification_send_dead_letters','NULL::bigint'),
        ('notification_replay_controls','NULL::bigint')
    ) AS scope(relation, application_key) LOOP
        EXECUTE format('ALTER TABLE public.%I ENABLE ROW LEVEL SECURITY', entry.relation);
        EXECUTE format('CREATE POLICY delivery_base_access ON public.%I USING (true)', entry.relation);
        EXECUTE format('CREATE POLICY delivery_application_scope ON public.%I AS RESTRICTIVE USING ((SELECT hello_delivery_internal.scoped_application()) IS NULL OR (SELECT hello_delivery_internal.scoped_application()) = %s)',
            entry.relation, entry.application_key);
    END LOOP;
END
$$;

CREATE OR REPLACE VIEW public.current_binding_routes WITH (security_barrier=true) AS
SELECT DISTINCT ON (application_id,binding_id) application_id,binding_id,expected_revision+1 AS revision,
       destination,cohort_id,resume_from,origin_epoch,source_epoch
FROM public.binding_route_grants
WHERE applied_at IS NOT NULL AND ((SELECT hello_delivery_internal.scoped_application()) IS NULL OR (SELECT hello_delivery_internal.scoped_application()) = application_id)
ORDER BY application_id,binding_id,expected_revision DESC;

CREATE OR REPLACE FUNCTION public.hold_delivery_dispatch(target_application bigint) RETURNS void
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
    UPDATE public.application_dispatch_policy SET hold_new=true
    WHERE application_id=target_application AND ((SELECT hello_delivery_internal.scoped_application()) IS NULL OR (SELECT hello_delivery_internal.scoped_application()) = application_id)
$$;
