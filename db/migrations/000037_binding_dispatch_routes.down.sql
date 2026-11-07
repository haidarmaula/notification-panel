DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM public.binding_route_grants) OR EXISTS(SELECT 1 FROM public.notification_delivery_intents WHERE audience_route_revision IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot discard route approval or audience evidence';
    END IF;
END $$;
DROP FUNCTION apply_binding_route(uuid,uuid);
DROP TRIGGER guard_audience_route_revision ON notification_delivery_intents;
DROP FUNCTION guard_audience_route_revision();
ALTER TABLE notification_delivery_intents DROP COLUMN audience_route_revision;
DROP VIEW current_binding_routes;
DROP TABLE binding_route_grants;
DROP FUNCTION guard_binding_route_grant();
