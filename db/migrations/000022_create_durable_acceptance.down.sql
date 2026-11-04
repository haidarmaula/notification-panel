DO $$
BEGIN
    LOCK TABLE public.notification_runtime, public.application_dispatch_policy, public.notifications,
        public.notification_send_keys, public.notification_send_outbox IN ACCESS EXCLUSIVE MODE;
    IF EXISTS(SELECT 1 FROM public.notifications WHERE deadline_at IS NOT NULL)
       OR EXISTS(SELECT 1 FROM public.notification_send_keys)
       OR EXISTS(SELECT 1 FROM public.notification_send_outbox)
    THEN
        RAISE EXCEPTION USING ERRCODE='23514', MESSAGE='cannot remove durable acceptance evidence';
    END IF;
    DROP TABLE public.notification_send_outbox;
    DROP TABLE public.notification_send_keys;
    DROP TABLE public.application_dispatch_policy;
    DROP TABLE public.notification_runtime;
    ALTER TABLE public.notifications DROP COLUMN queued_at, DROP COLUMN deadline_at,
        DROP COLUMN origin_epoch, DROP COLUMN source_epoch, DROP COLUMN lifecycle_reason;
END;
$$;
