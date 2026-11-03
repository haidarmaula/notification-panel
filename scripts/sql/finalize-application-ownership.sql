-- Offline cutover only. Execute as one statement after reviewed ownership backfill.
-- Any unresolved row aborts the entire statement.
DO $$
BEGIN
    ALTER TABLE public.users ALTER COLUMN application_id SET NOT NULL;
    ALTER TABLE public.device_tokens ALTER COLUMN application_id SET NOT NULL;
    ALTER TABLE public.templates ALTER COLUMN application_id SET NOT NULL;
    ALTER TABLE public.segments ALTER COLUMN application_id SET NOT NULL;
    ALTER TABLE public.segment_members ALTER COLUMN application_id SET NOT NULL;
    ALTER TABLE public.notifications ALTER COLUMN application_id SET NOT NULL;
    ALTER TABLE public.upload_batches ALTER COLUMN application_id SET NOT NULL;
    ALTER TABLE public.upload_batch_rows ALTER COLUMN application_id SET NOT NULL;
    ALTER TABLE public.notification_targets ALTER COLUMN application_id SET NOT NULL;
    ALTER TABLE public.notification_deliveries ALTER COLUMN application_id SET NOT NULL;
    ALTER TABLE public.notification_reads ALTER COLUMN application_id SET NOT NULL;
END;
$$;
