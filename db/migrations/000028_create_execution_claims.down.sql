DO $$
BEGIN
    LOCK TABLE public.notifications,public.notification_delivery_intents IN ACCESS EXCLUSIVE MODE;
    IF EXISTS(SELECT 1 FROM public.notifications WHERE lease_generation>0 OR lease_owner IS NOT NULL)
       OR EXISTS(SELECT 1 FROM public.notification_delivery_intents WHERE attempt_id IS NOT NULL OR attempt_count>0)
    THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='cannot remove retained execution evidence';
    END IF;
    DROP INDEX public.idx_execution_lease_recovery;
    DROP INDEX public.uq_delivery_attempt_id;
    ALTER TABLE public.notification_delivery_intents
        DROP CONSTRAINT delivery_attempt_pair,
        DROP COLUMN attempt_resolved_at,
        DROP COLUMN last_attempt_at,
        DROP COLUMN claim_generation,
        DROP COLUMN attempt_id;
    ALTER TABLE public.notifications
        DROP CONSTRAINT execution_lease_pair,
        DROP COLUMN lease_until,
        DROP COLUMN lease_generation,
        DROP COLUMN lease_owner;
END;
$$;
