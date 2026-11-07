-- Opt-in only. Existing M32 grants, checkpoints, function owner and ACLs survive.
ALTER TABLE retention_policies DROP CONSTRAINT retention_policies_record_class_check;
ALTER TABLE retention_policies ADD CONSTRAINT retention_policies_record_class_check
    CHECK(record_class IN ('origin_nonce','receipt_request','send_key'));
ALTER TABLE retention_batches ADD COLUMN send_page_end bigint,
    ADD COLUMN send_page_full boolean, ADD COLUMN send_candidates bigint[],
    ADD CONSTRAINT retention_send_page_check CHECK (
        (send_page_end IS NULL AND send_page_full IS NULL AND send_candidates IS NULL)
        OR (send_page_end IS NOT NULL AND send_page_end>=0 AND send_page_full IS NOT NULL AND
            send_candidates IS NOT NULL AND cardinality(send_candidates)=preview_count)
    );
CREATE INDEX idx_send_retention_page ON notification_send_keys(application_id,notification_id);
CREATE INDEX idx_send_retention_unfinished ON notification_delivery_intents(notification_id)
    WHERE status='PENDING' OR (attempt_id IS NOT NULL AND attempt_resolved_at IS NULL);
CREATE INDEX idx_send_retention_updates ON notification_update_outbox(application_id,notification_id);
CREATE INDEX idx_send_retention_evidence ON notification_send_dead_letters(original_key)
    WHERE octet_length(original_key)<=32;

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
    page_start bigint := 0;
    page_end bigint;
    page_full boolean;
    candidates bigint[];
BEGIN
    IF current_setting('transaction_isolation')<>'read committed'
       OR p_apply IS NULL OR p_batch IS NULL OR p_batch<1
       OR pg_has_role(session_user,current_user,'MEMBER') THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='retention grant unavailable';
    END IF;
    -- Cheap preliminary check before acquiring any control or application lock.
    PERFORM 1 FROM public.retention_grants j JOIN public.applications a ON a.id=j.application_id
    WHERE j.id=p_grant AND j.operator_role=session_user::regrole::oid AND a.public_id=p_project
        AND j.origin_epoch=p_origin AND j.source_epoch=p_source AND j.record_class=p_class
        AND (p_class<>'send_key' OR NOT p_apply OR (j.allow_apply AND j.revoked_at IS NULL
            AND j.expires_at>clock_timestamp() AND p_batch<=j.max_batches
            AND EXISTS(SELECT 1 FROM public.retention_policies p WHERE p.application_id=j.application_id
                AND p.record_class=j.record_class AND p.policy_version=j.policy_version AND NOT p.held)
            AND EXISTS(SELECT 1 FROM public.retention_batches b WHERE b.grant_id=j.id AND b.batch_no=p_batch)));
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='retention grant unavailable';
    END IF;
    -- Quarantine.Store takes FOR SHARE before inserting a physical position.
    -- Take the conflicting lock first, then read dependencies in a fresh statement.
    IF p_class='send_key' AND p_apply THEN
        PERFORM 1 FROM public.notification_runtime
        WHERE singleton AND NOT sealed AND NOT migration_hold AND origin_epoch=p_origin AND source_epoch=p_source
        FOR NO KEY UPDATE;
    ELSE
        PERFORM 1 FROM public.notification_runtime
        WHERE singleton AND NOT sealed AND NOT migration_hold AND origin_epoch=p_origin AND source_epoch=p_source
        FOR SHARE;
    END IF;
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
    SELECT b.preview_at,b.preview_count,b.applied_at,b.removed_count,b.outcome
    INTO checkpoint.preview_at,checkpoint.preview_count,checkpoint.applied_at,checkpoint.removed_count,checkpoint.outcome
    FROM public.retention_batches b WHERE b.grant_id=g.id AND b.batch_no=p_batch;
    IF FOUND AND (NOT p_apply OR checkpoint.applied_at IS NOT NULL) THEN
        RETURN QUERY SELECT checkpoint.outcome,coalesce(checkpoint.removed_count,checkpoint.preview_count);
        RETURN;
    END IF;
    IF (p_apply AND checkpoint.preview_at IS NULL) OR (p_batch>1 AND NOT EXISTS(
        SELECT 1 FROM public.retention_batches b WHERE b.grant_id=g.id AND b.batch_no=p_batch-1 AND b.outcome='more'
    )) THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='retention checkpoint unavailable';
    END IF;

    IF g.record_class='send_key' THEN
        IF p_apply THEN
            SELECT b.send_page_end,b.send_page_full,b.send_candidates
            INTO STRICT checkpoint.send_page_end,checkpoint.send_page_full,checkpoint.send_candidates
            FROM public.retention_batches b WHERE b.grant_id=g.id AND b.batch_no=p_batch AND b.send_page_end IS NOT NULL;
        END IF;
        IF p_batch>1 THEN
            SELECT b.send_page_end INTO STRICT page_start FROM public.retention_batches b
            WHERE b.grant_id=g.id AND b.batch_no=p_batch-1 AND b.send_page_end IS NOT NULL;
        END IF;
        WITH page AS MATERIALIZED (
            SELECT k.* FROM public.notification_send_keys k
            WHERE k.application_id=project_id AND k.notification_id>page_start
                AND (NOT p_apply OR k.notification_id<=checkpoint.send_page_end)
            ORDER BY k.notification_id LIMIT g.batch_size
        ), eligible AS MATERIALIZED (
            SELECT k.* FROM page k
            JOIN public.notifications n ON n.application_id=k.application_id AND n.id=k.notification_id
            JOIN public.notification_preparations p ON p.application_id=n.application_id AND p.notification_id=n.id
                AND p.origin_epoch=n.origin_epoch AND p.source_epoch=n.source_epoch
            WHERE (NOT p_apply OR k.notification_id=ANY(checkpoint.send_candidates))
                AND k.expires_at<=g.cutoff_at AND k.deadline_at<=g.cutoff_at AND n.completed_at<=g.cutoff_at
                AND k.queued_at=n.queued_at AND k.deadline_at=n.deadline_at
                AND n.origin_epoch=g.origin_epoch AND n.source_epoch=g.source_epoch
                AND n.status IN ('COMPLETED','FAILED') AND n.lease_owner IS NULL AND n.lease_until IS NULL
                -- Acceptance creates one original key for the DRAFT transition.
                -- Preserve ambiguous imported duplicates, including a group crossing a page boundary.
                AND NOT EXISTS(SELECT 1 FROM public.notification_send_keys other
                    WHERE other.application_id=k.application_id AND other.notification_id=k.notification_id OFFSET 1)
                AND NOT EXISTS(SELECT 1 FROM public.notification_delivery_intents d
                    WHERE d.notification_id=n.id AND (d.status='PENDING' OR (d.attempt_id IS NOT NULL AND d.attempt_resolved_at IS NULL)))
                AND NOT EXISTS(SELECT 1 FROM public.notification_send_outbox o WHERE o.notification_id=n.id)
                AND NOT EXISTS(SELECT 1 FROM public.notification_update_outbox o
                    WHERE o.application_id=n.application_id AND o.notification_id=n.id)
                -- A valid v1 replay has exactly this physical key. Include every
                -- source/incarnation and separately check all attributed attempts.
                AND NOT EXISTS (
                    SELECT 1 FROM (
                        (SELECT d.log_source_id,d.original_partition,d.original_offset
                         FROM public.notification_send_dead_letters d
                         WHERE octet_length(d.original_key)<=32
                             AND d.original_key=convert_to('notification_'||n.id::text,'UTF8') LIMIT 65)
                        UNION ALL
                        (SELECT a.log_source_id,a.original_partition,a.original_offset
                         FROM public.notification_replay_attempts a
                         WHERE a.application_id=n.application_id AND a.notification_id=n.id LIMIT 65)
                    ) e
                    LEFT JOIN public.notification_replay_controls c USING(log_source_id,original_partition,original_offset)
                    LEFT JOIN public.notification_replay_attempts resolved
                        ON resolved.application_id=n.application_id AND c.resolved_application_id=n.application_id
                        AND resolved.request_id=c.resolved_request_id AND resolved.log_source_id=e.log_source_id
                        AND resolved.original_partition=e.original_partition AND resolved.original_offset=e.original_offset
                        AND resolved.outcome=c.resolved_outcome AND resolved.notification_id=n.id
                        AND resolved.origin_epoch=n.origin_epoch AND resolved.source_epoch=n.source_epoch
                        AND c.active_claim_token IS NULL
                    HAVING count(*)>64 OR count(*)<>count(resolved.notification_id)
                )
        ), deletion AS (
            DELETE FROM public.notification_send_keys k USING eligible e
            WHERE p_apply AND k.application_id=e.application_id AND k.notification_id=e.notification_id
                AND k.staff_user_id=e.staff_user_id AND k.key_source=e.key_source AND k.key=e.key
            RETURNING 1
        )
        SELECT (SELECT count(*) FROM eligible),(SELECT count(*) FROM deletion),0,
            coalesce((SELECT max(notification_id) FROM page),page_start),
            (SELECT count(*)=g.batch_size FROM page),
            ARRAY(SELECT notification_id FROM eligible ORDER BY notification_id)
        INTO selected,removed,inconsistent,page_end,page_full,candidates;
    ELSIF g.record_class='origin_nonce' THEN
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
    ELSIF g.record_class='receipt_request' THEN
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
    ELSE
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='retention class unavailable';
    END IF;
    IF inconsistent<>0 OR (p_apply AND (removed<>selected OR removed>checkpoint.preview_count))
       OR g.expires_at<=clock_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='retention evidence mismatch or grant expired';
    END IF;
    IF NOT p_apply THEN
        IF g.record_class='send_key' THEN
            INSERT INTO public.retention_batches(grant_id,batch_no,preview_count,outcome,send_page_end,send_page_full,send_candidates)
            VALUES(g.id,p_batch,selected,'preview',page_end,page_full,candidates);
        ELSE
            INSERT INTO public.retention_batches(grant_id,batch_no,preview_count,outcome) VALUES(g.id,p_batch,selected,'preview');
        END IF;
        RETURN QUERY SELECT 'preview'::text,selected;
    ELSE
        result := CASE WHEN (g.record_class='send_key' AND NOT checkpoint.send_page_full)
            OR (g.record_class<>'send_key' AND removed<g.batch_size) THEN 'complete' WHEN p_batch=g.max_batches THEN 'budget_exhausted' ELSE 'more' END;
        UPDATE public.retention_batches b SET applied_at=clock_timestamp(),removed_count=removed,outcome=result
        WHERE b.grant_id=g.id AND b.batch_no=p_batch;
        RETURN QUERY SELECT result,removed;
    END IF;
END;
$$;
