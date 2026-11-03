-- Operator-only, reviewed one-app ownership transition. Does NOT enable dispatch.
-- Run inside BEGIN ISOLATION LEVEL SERIALIZABLE, after quiescing all writers.
-- Required transaction-local settings (bind values, never concatenate input):
-- hello.scope_target_public_id = explicit reviewed PROVISIONING UUID
-- hello.scope_mapping_ack = one-app-ownership-reviewed
DO $$
DECLARE
    target_id bigint;
    relation_name text;
    conflicting boolean;
BEGIN
    IF current_setting('transaction_isolation') <> 'serializable' THEN
        RAISE EXCEPTION USING ERRCODE='23514', MESSAGE='serializable maintenance transaction required';
    END IF;
    IF current_setting('hello.scope_mapping_ack', true) IS DISTINCT FROM 'one-app-ownership-reviewed' THEN
        RAISE EXCEPTION USING ERRCODE='23514', MESSAGE='reviewed one-app ownership acknowledgement required';
    END IF;
    -- Fixed relation list, same lock order on every run; blocks concurrent DML.
    LOCK TABLE public.applications, public.users, public.device_tokens, public.templates, public.segments, public.segment_members, public.notifications, public.upload_batches, public.upload_batch_rows, public.notification_targets, public.notification_deliveries, public.notification_reads, public.audit_logs IN SHARE ROW EXCLUSIVE MODE;
    SELECT id INTO target_id FROM public.applications
        WHERE public_id = nullif(current_setting('hello.scope_target_public_id', true), '')::uuid
          AND status='PROVISIONING' FOR UPDATE;
    IF target_id IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='23514', MESSAGE='explicit provisioning application required';
    END IF;
    FOREACH relation_name IN ARRAY ARRAY['users','device_tokens','templates','segments','segment_members','notifications','upload_batches','upload_batch_rows','notification_targets','notification_deliveries','notification_reads'] LOOP
        EXECUTE format('SELECT EXISTS(SELECT 1 FROM public.%I WHERE application_id IS NOT NULL AND application_id<>$1)', relation_name)
            INTO conflicting USING target_id;
        IF conflicting THEN
            RAISE EXCEPTION USING ERRCODE='23514', MESSAGE='existing business ownership conflicts with one-app mapping';
        END IF;
    END LOOP;
    IF EXISTS(SELECT 1 FROM public.notification_deliveries d JOIN public.device_tokens t ON t.id=d.device_token_id WHERE d.user_id<>t.user_id) THEN
        RAISE EXCEPTION USING ERRCODE='23514', MESSAGE='legacy delivery device/user conflict requires review';
    END IF;
    IF EXISTS(SELECT 1 FROM public.audit_logs a WHERE a.application_id IS NOT NULL AND a.application_id<>target_id
              AND a.entity_type IN ('segment','notification')) THEN
        RAISE EXCEPTION USING ERRCODE='23514', MESSAGE='existing business audit ownership conflicts with one-app mapping';
    END IF;
    UPDATE public.users SET application_id=target_id WHERE application_id IS NULL;
    UPDATE public.templates SET application_id=target_id WHERE application_id IS NULL;
    UPDATE public.segments SET application_id=target_id WHERE application_id IS NULL;
    UPDATE public.upload_batches SET application_id=target_id WHERE application_id IS NULL;
    UPDATE public.notifications SET application_id=target_id WHERE application_id IS NULL;
    UPDATE public.device_tokens SET application_id=target_id WHERE application_id IS NULL;
    UPDATE public.segment_members SET application_id=target_id WHERE application_id IS NULL;
    UPDATE public.upload_batch_rows SET application_id=target_id WHERE application_id IS NULL;
    UPDATE public.notification_targets SET application_id=target_id WHERE application_id IS NULL;
    UPDATE public.notification_deliveries SET application_id=target_id WHERE application_id IS NULL;
    UPDATE public.notification_reads SET application_id=target_id WHERE application_id IS NULL;
    -- Map only resolvable known business entities; do not modify snapshot JSON.
    UPDATE public.audit_logs a SET application_id=target_id
        WHERE a.application_id IS NULL AND
            ((a.entity_type='segment' AND EXISTS(SELECT 1 FROM public.segments s WHERE s.id=a.entity_id AND s.application_id=target_id))
          OR (a.entity_type='notification' AND EXISTS(SELECT 1 FROM public.notifications n WHERE n.id=a.entity_id AND n.application_id=target_id)));
END;
$$;
