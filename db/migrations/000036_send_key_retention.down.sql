-- Rollback requires an empty send-key policy/evidence domain. Mobile policies
-- and already-issued grants continue using the migration 000032 implementation.
LOCK TABLE retention_policies,retention_grants,retention_batches IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS(SELECT 1 FROM public.retention_policies WHERE record_class='send_key')
        OR EXISTS(SELECT 1 FROM public.retention_batches WHERE send_page_end IS NOT NULL) THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='cannot remove send retention policy or evidence';
    END IF;
END;
$$;
ALTER TABLE retention_policies DROP CONSTRAINT retention_policies_record_class_check;
ALTER TABLE retention_policies ADD CONSTRAINT retention_policies_record_class_check
    CHECK(record_class IN ('origin_nonce','receipt_request'));
ALTER TABLE retention_batches DROP COLUMN send_page_end,DROP COLUMN send_page_full,DROP COLUMN send_candidates;
DROP INDEX idx_send_retention_page,idx_send_retention_unfinished,idx_send_retention_updates,idx_send_retention_evidence;

CREATE OR REPLACE FUNCTION run_retention_batch(
    p_grant uuid, p_project uuid, p_origin uuid, p_source uuid,
    p_class text, p_batch integer, p_apply boolean
) RETURNS TABLE(outcome text,quantity integer)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,pg_temp SET lock_timeout='250ms'
AS $$
DECLARE
    g public.retention_grants%ROWTYPE;
    policy public.retention_policies%ROWTYPE;
    checkpoint public.retention_batches%ROWTYPE;
    selected integer;
    removed integer;
    inconsistent integer;
    result text;
    project_id bigint;
BEGIN
    IF current_setting('transaction_isolation')<>'read committed'
       OR p_apply IS NULL OR p_batch IS NULL OR p_batch<1
       OR pg_has_role(session_user,current_user,'MEMBER') THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='retention grant unavailable';
    END IF;
    -- Cheap preliminary check before acquiring any control or application lock.
    PERFORM 1 FROM public.retention_grants j JOIN public.applications a ON a.id=j.application_id
    WHERE j.id=p_grant AND j.operator_role=session_user::regrole::oid AND a.public_id=p_project
        AND j.origin_epoch=p_origin AND j.source_epoch=p_source AND j.record_class=p_class;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='retention grant unavailable';
    END IF;
    PERFORM 1 FROM public.notification_runtime
    WHERE singleton AND NOT sealed AND NOT migration_hold AND origin_epoch=p_origin AND source_epoch=p_source
    FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='retention held';
    END IF;
    -- Serializes the expiry check with live receipts and signed origin assertions.
    SELECT a.id INTO STRICT project_id FROM public.applications a WHERE a.public_id=p_project FOR NO KEY UPDATE;
    SELECT * INTO policy FROM public.retention_policies p
    WHERE p.application_id=project_id AND p.record_class=p_class AND NOT p.held FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='retention held';
    END IF;
    SELECT * INTO g FROM public.retention_grants j
    WHERE j.id=p_grant AND j.application_id=project_id AND j.record_class=p_class
        AND j.policy_version=policy.policy_version AND j.operator_role=session_user::regrole::oid
        AND j.origin_epoch=p_origin AND j.source_epoch=p_source AND j.revoked_at IS NULL
        AND j.issued_at<=clock_timestamp() AND j.expires_at>clock_timestamp()
        AND j.cutoff_at<=j.issued_at-make_interval(secs=>policy.retain_after_expiry_seconds)
        AND p_batch<=j.max_batches AND (NOT p_apply OR j.allow_apply)
    FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='retention grant unavailable';
    END IF;
    SELECT * INTO checkpoint FROM public.retention_batches b WHERE b.grant_id=g.id AND b.batch_no=p_batch;
    IF FOUND AND (NOT p_apply OR checkpoint.applied_at IS NOT NULL) THEN
        RETURN QUERY SELECT checkpoint.outcome,coalesce(checkpoint.removed_count,checkpoint.preview_count);
        RETURN;
    END IF;
    IF (p_apply AND checkpoint.preview_at IS NULL) OR (p_batch>1 AND NOT EXISTS(
        SELECT 1 FROM public.retention_batches b WHERE b.grant_id=g.id AND b.batch_no=p_batch-1 AND b.outcome='more'
    )) THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='retention checkpoint unavailable';
    END IF;

    IF g.record_class='origin_nonce' THEN
        WITH page AS MATERIALIZED (
            SELECT n.issuer,n.nonce_digest FROM public.mobile_origin_uses n
            WHERE n.application_public_id=p_project AND n.expires_at<=g.cutoff_at
            ORDER BY n.expires_at,n.issuer,n.nonce_digest LIMIT g.batch_size
        ), deletion AS (
            DELETE FROM public.mobile_origin_uses n USING page
            WHERE p_apply AND n.application_public_id=p_project AND n.issuer=page.issuer
                AND n.nonce_digest=page.nonce_digest AND n.expires_at<=g.cutoff_at
            RETURNING 1
        ) SELECT (SELECT count(*) FROM page),(SELECT count(*) FROM deletion),0 INTO selected,removed,inconsistent;
    ELSE
        WITH page AS MATERIALIZED (
            SELECT r.user_id,r.client_event_id,r.delivery_id,r.expires_unix FROM public.notification_receipt_requests r
            WHERE r.application_id=project_id AND r.expires_unix<=floor(extract(epoch FROM g.cutoff_at))::bigint
            ORDER BY r.expires_unix,r.user_id,r.client_event_id LIMIT g.batch_size
        ), verified AS MATERIALIZED (
            SELECT page.user_id,page.client_event_id FROM page JOIN public.notification_receipt_metadata m
                ON m.application_id=project_id AND m.delivery_id=page.delivery_id AND m.expires_unix=page.expires_unix
        ), deletion AS (
            DELETE FROM public.notification_receipt_requests r USING verified
            WHERE p_apply AND r.application_id=project_id AND r.user_id=verified.user_id AND r.client_event_id=verified.client_event_id
            RETURNING 1
        ) SELECT (SELECT count(*) FROM page),(SELECT count(*) FROM deletion),
            (SELECT count(*) FROM page)-(SELECT count(*) FROM verified) INTO selected,removed,inconsistent;
    END IF;
    IF inconsistent<>0 OR (p_apply AND (removed<>selected OR removed>checkpoint.preview_count))
       OR g.expires_at<=clock_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='retention evidence mismatch or grant expired';
    END IF;
    IF NOT p_apply THEN
        INSERT INTO public.retention_batches(grant_id,batch_no,preview_count,outcome) VALUES(g.id,p_batch,selected,'preview');
        RETURN QUERY SELECT 'preview'::text,selected;
    ELSE
        result := CASE WHEN removed<g.batch_size THEN 'complete' WHEN p_batch=g.max_batches THEN 'budget_exhausted' ELSE 'more' END;
        UPDATE public.retention_batches b SET applied_at=clock_timestamp(),removed_count=removed,outcome=result
        WHERE b.grant_id=g.id AND b.batch_no=p_batch;
        RETURN QUERY SELECT result,removed;
    END IF;
END;
$$;
