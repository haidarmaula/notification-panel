DO $$
BEGIN
    LOCK TABLE public.staff_session_control,public.staff_sessions,public.staff_users IN ACCESS EXCLUSIVE MODE;
    IF EXISTS(SELECT 1 FROM public.staff_sessions) OR EXISTS(SELECT 1 FROM public.staff_users WHERE auth_epoch<>1) THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='cannot remove staff session or revocation evidence';
    END IF;
    DROP TABLE public.staff_sessions,public.staff_session_control;
    DROP TRIGGER staff_auth_epoch_change ON public.staff_users;
    DROP FUNCTION public.advance_staff_auth_epoch();
    ALTER TABLE public.staff_users DROP COLUMN auth_epoch;
END;
$$;
