-- Rollback is for empty installations only. Never erase binding history.
DO $$
BEGIN
    LOCK TABLE public.mobile_installations, public.installation_bindings IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM public.mobile_installations)
        OR EXISTS (SELECT 1 FROM public.installation_bindings)
    THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'cannot remove populated installation schema';
    END IF;
    DROP TABLE public.installation_bindings;
    DROP TABLE public.mobile_installations;
    DROP FUNCTION public.guard_installation_binding_update();
    DROP FUNCTION public.guard_mobile_installation_update();
END;
$$;
