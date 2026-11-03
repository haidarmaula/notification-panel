DO $$
BEGIN
    LOCK TABLE public.mobile_identities IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM public.mobile_identities) THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'cannot remove populated mobile identity mapping';
    END IF;
    DROP TABLE public.mobile_identities;
END;
$$;
