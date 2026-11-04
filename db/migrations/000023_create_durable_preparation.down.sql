DO $$
BEGIN
    LOCK TABLE public.notifications,public.notification_preparations,public.notification_delivery_intents
        IN ACCESS EXCLUSIVE MODE;
    IF EXISTS(SELECT 1 FROM public.notification_preparations)
       OR EXISTS(SELECT 1 FROM public.notification_delivery_intents)
       OR EXISTS(SELECT 1 FROM public.notifications WHERE preparation_hold_reason IS NOT NULL)
    THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='cannot remove durable preparation evidence';
    END IF;
    DROP TABLE public.notification_delivery_intents;
    DROP TABLE public.notification_preparations;
    DROP FUNCTION public.guard_preparation_update();
    DROP FUNCTION public.guard_delivery_intent_update();
    ALTER TABLE public.installation_bindings DROP CONSTRAINT uq_binding_intent_owner;
    DROP INDEX public.idx_scoped_preparation_recovery;
    ALTER TABLE public.notifications DROP COLUMN preparation_hold_reason,DROP COLUMN preparation_next_attempt_at;
END;
$$;
