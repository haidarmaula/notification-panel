DO $$
BEGIN
    LOCK TABLE public.mobile_origin_uses IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM public.mobile_origin_uses WHERE expires_at > clock_timestamp()) THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'cannot remove live origin replay markers';
    END IF;
    DROP TABLE public.mobile_origin_uses;
END;
$$;
