DO $$
BEGIN
    LOCK TABLE public.application_onboarding_held_dispatch_bootstraps IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT FROM public.application_onboarding_held_dispatch_bootstraps)
    THEN RAISE EXCEPTION 'held dispatch bootstrap history requires M60'; END IF;
    DROP FUNCTION public.bootstrap_onboarding_held_dispatch(uuid,text,bigint,integer,integer);
    DROP TABLE public.application_onboarding_held_dispatch_bootstraps;
END;
$$;
