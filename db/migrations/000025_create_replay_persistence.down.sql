DO $$
BEGIN
    LOCK TABLE public.notification_replay_requests,public.notification_replay_attempts,
        public.notification_replay_controls IN ACCESS EXCLUSIVE MODE;
    IF EXISTS(SELECT 1 FROM public.notification_replay_requests)
       OR EXISTS(SELECT 1 FROM public.notification_replay_attempts)
       OR EXISTS(SELECT 1 FROM public.notification_replay_controls)
    THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='cannot remove retained replay evidence';
    END IF;
    DROP TABLE public.notification_replay_controls;
    DROP TABLE public.notification_replay_attempts;
    DROP TABLE public.notification_replay_requests;
    DROP FUNCTION public.guard_replay_control_update();
    DROP FUNCTION public.guard_replay_attempt_update();
    DROP FUNCTION public.guard_replay_attempt_insert();
    DROP FUNCTION public.guard_replay_request_update();
END;
$$;
