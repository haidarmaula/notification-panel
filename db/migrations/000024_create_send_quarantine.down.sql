DO $$
BEGIN
    LOCK TABLE public.notification_log_sources,public.notification_send_dead_letters IN ACCESS EXCLUSIVE MODE;
    IF EXISTS(SELECT 1 FROM public.notification_send_dead_letters)
       OR EXISTS(SELECT 1 FROM public.notification_log_sources WHERE active)
    THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='cannot remove active send source or quarantine evidence';
    END IF;
    DROP TABLE public.notification_send_dead_letters;
    DROP TABLE public.notification_log_sources;
    DROP FUNCTION public.guard_send_log_source_update();
END;
$$;
