DO $$
BEGIN
    LOCK TABLE public.notification_delivery_intents,public.notification_receipt_metadata IN ACCESS EXCLUSIVE MODE;
    IF EXISTS(SELECT 1 FROM public.notification_receipt_metadata) THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='cannot remove retained receipt metadata';
    END IF;
    DROP TABLE public.notification_receipt_metadata;
    DROP FUNCTION public.guard_receipt_metadata();
    ALTER TABLE public.notification_delivery_intents DROP CONSTRAINT uq_delivery_application_identity;
END;
$$;
