DO $$
BEGIN
    LOCK TABLE public.notification_delivery_intents,public.notification_update_outbox IN ACCESS EXCLUSIVE MODE;
    IF EXISTS(SELECT 1 FROM public.notification_update_outbox)
       OR EXISTS(SELECT 1 FROM public.notification_delivery_intents WHERE last_result_fingerprint IS NOT NULL
           OR provider_accepted_at IS NOT NULL OR observed_delivered_at IS NOT NULL OR observed_opened_at IS NOT NULL
           OR failed_at IS NOT NULL OR terminal_reason IS NOT NULL OR delivery_version>0)
    THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='cannot remove retained delivery result evidence';
    END IF;
    DROP TABLE public.notification_update_outbox;
    DROP FUNCTION public.guard_delivery_update_envelope();
    ALTER TABLE public.notification_delivery_intents
        DROP CONSTRAINT uq_delivery_update_identity,
        DROP CONSTRAINT delivery_provider_evidence_pair,DROP CONSTRAINT delivery_result_evidence_pair,
        DROP COLUMN provider_message_id,DROP COLUMN provider_accepted_at,
        DROP COLUMN observed_delivered_at,DROP COLUMN observed_opened_at,
        DROP COLUMN failed_at,DROP COLUMN terminal_reason,
        DROP COLUMN last_result_disposition,DROP COLUMN last_result_reason,DROP COLUMN last_result_fingerprint;
END;
$$;
