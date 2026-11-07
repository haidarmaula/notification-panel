DO $$
BEGIN
    LOCK TABLE public.notification_receipt_requests,public.notification_observations IN ACCESS EXCLUSIVE MODE;
    IF EXISTS(SELECT 1 FROM public.notification_receipt_requests) OR EXISTS(SELECT 1 FROM public.notification_observations) THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='cannot remove retained receipt evidence';
    END IF;
    DROP TABLE public.notification_receipt_requests,public.notification_observations;
END;
$$;
