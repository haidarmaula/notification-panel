DO $$
DECLARE relation_name text;
BEGIN
    LOCK TABLE public.application_worker_preflight_scopes IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT FROM public.application_worker_preflight_scopes) THEN
        RAISE EXCEPTION 'approved worker scopes require explicit recovery before removing delivery isolation';
    END IF;

    FOREACH relation_name IN ARRAY ARRAY[
        'applications','application_dispatch_policy','notifications','notification_preparations',
        'users','notification_delivery_intents','mobile_installations','installation_bindings',
        'notification_receipt_metadata','notification_send_outbox','notification_update_outbox',
        'notification_send_dead_letters','notification_replay_controls'
    ] LOOP
        EXECUTE format('DROP POLICY delivery_application_scope ON public.%I', relation_name);
        EXECUTE format('DROP POLICY delivery_base_access ON public.%I', relation_name);
        EXECUTE format('ALTER TABLE public.%I DISABLE ROW LEVEL SECURITY', relation_name);
    END LOOP;
END
$$;

ALTER VIEW public.current_binding_routes RESET (security_barrier);
CREATE OR REPLACE VIEW public.current_binding_routes AS
SELECT DISTINCT ON (application_id,binding_id) application_id,binding_id,expected_revision+1 AS revision,
       destination,cohort_id,resume_from,origin_epoch,source_epoch
FROM public.binding_route_grants WHERE applied_at IS NOT NULL
ORDER BY application_id,binding_id,expected_revision DESC;

CREATE OR REPLACE FUNCTION public.hold_delivery_dispatch(target_application bigint) RETURNS void
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
    UPDATE public.application_dispatch_policy SET hold_new=true WHERE application_id=target_application
$$;

DROP FUNCTION hello_delivery_internal.scoped_application();
DROP SCHEMA hello_delivery_internal;
