DO $$
BEGIN
    LOCK TABLE public.application_onboarding_lifecycle_delegations IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT FROM public.application_onboarding_lifecycle_delegations) THEN
        RAISE EXCEPTION 'onboarding lifecycle delegations require explicit recovery before rollback';
    END IF;
    DROP FUNCTION public.apply_onboarding_first_activation(uuid,uuid,uuid);
    DROP TRIGGER require_onboarding_lifecycle_completion ON public.application_lifecycle_grants;
    DROP FUNCTION public.require_onboarding_lifecycle_completion();
    DROP TABLE public.application_onboarding_lifecycle_delegations;
END;
$$;
