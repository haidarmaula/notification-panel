DO $$
BEGIN
    LOCK TABLE public.application_worker_preflight_scopes IN ACCESS EXCLUSIVE MODE;
    IF EXISTS(SELECT FROM public.application_worker_preflight_scopes)
    THEN
        RAISE EXCEPTION 'approved worker preflight scopes require explicit recovery before rollback';
    END IF;
    DROP FUNCTION public.check_application_onboarding_worker_preflight(uuid,uuid,bytea,text,text,bytea,text);
    DROP TABLE public.application_worker_preflight_scopes;
    DROP FUNCTION public.protect_application_worker_preflight_scope();
END;
$$;
