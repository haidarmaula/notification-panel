DO $$
BEGIN
    LOCK TABLE public.notification_replay_grants IN ACCESS EXCLUSIVE MODE;
    IF EXISTS(SELECT 1 FROM public.notification_replay_grants) THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='cannot remove retained replay grants';
    END IF;
    DROP TABLE public.notification_replay_grants;
    DROP FUNCTION public.guard_replay_grant_update();
END;
$$;
