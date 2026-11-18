DO $$
BEGIN
    LOCK TABLE public.application_runtime_component_scopes, public.application_runtime_acknowledgements IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT FROM public.application_runtime_component_scopes) OR
       EXISTS (SELECT FROM public.application_runtime_acknowledgements) THEN
        RAISE EXCEPTION 'runtime acknowledgement evidence requires explicit recovery before rollback';
    END IF;
    DROP FUNCTION public.acknowledge_application_runtime(uuid,bigint,text,bytea,uuid);
    DROP TABLE public.application_runtime_component_scopes;
END
$$;
