-- Issuers and hold administrators use separate roles. No policy or grant is seeded.
CREATE TABLE retention_policies (
    application_id bigint NOT NULL REFERENCES applications(id) ON DELETE RESTRICT,
    record_class text NOT NULL CHECK (record_class IN ('origin_nonce','receipt_request')),
    policy_version uuid NOT NULL,
    retain_after_expiry_seconds integer NOT NULL CHECK (retain_after_expiry_seconds>=0),
    held boolean NOT NULL DEFAULT true,
    PRIMARY KEY(application_id,record_class)
);

CREATE TABLE retention_grants (
    id uuid PRIMARY KEY,
    application_id bigint NOT NULL,
    record_class text NOT NULL,
    policy_version uuid NOT NULL,
    -- OID pins the login across rename and prevents reuse of a dropped role's name.
    operator_role oid NOT NULL,
    origin_epoch uuid NOT NULL,
    source_epoch uuid NOT NULL,
    cutoff_at timestamptz NOT NULL,
    issued_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    batch_size integer NOT NULL CHECK (batch_size BETWEEN 1 AND 500),
    max_batches integer NOT NULL CHECK (max_batches BETWEEN 1 AND 1000),
    allow_apply boolean NOT NULL DEFAULT false,
    revoked_at timestamptz,
    CHECK (isfinite(cutoff_at) AND isfinite(issued_at) AND isfinite(expires_at)),
    CHECK (cutoff_at<=issued_at AND expires_at>issued_at AND (revoked_at IS NULL OR revoked_at>=issued_at)),
    FOREIGN KEY(application_id,record_class) REFERENCES retention_policies(application_id,record_class) ON DELETE RESTRICT
);
CREATE INDEX idx_retention_grants_policy ON retention_grants(application_id,record_class);

CREATE FUNCTION guard_retention_grant() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF ROW(NEW.id,NEW.application_id,NEW.record_class,NEW.policy_version,NEW.operator_role,NEW.origin_epoch,
           NEW.source_epoch,NEW.cutoff_at,NEW.issued_at,NEW.expires_at,NEW.batch_size,NEW.max_batches,NEW.allow_apply)
       IS DISTINCT FROM
       ROW(OLD.id,OLD.application_id,OLD.record_class,OLD.policy_version,OLD.operator_role,OLD.origin_epoch,
           OLD.source_epoch,OLD.cutoff_at,OLD.issued_at,OLD.expires_at,OLD.batch_size,OLD.max_batches,OLD.allow_apply)
       OR (OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at) THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='retention grant scope and revocation are immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER guard_retention_grant_update BEFORE UPDATE ON retention_grants
    FOR EACH ROW EXECUTE FUNCTION guard_retention_grant();

CREATE TABLE retention_batches (
    grant_id uuid NOT NULL REFERENCES retention_grants(id) ON DELETE RESTRICT,
    batch_no integer NOT NULL CHECK (batch_no>0),
    preview_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    preview_count integer NOT NULL CHECK (preview_count BETWEEN 0 AND 500),
    applied_at timestamptz,
    removed_count integer CHECK (removed_count BETWEEN 0 AND preview_count),
    outcome text NOT NULL CHECK (outcome IN ('preview','more','complete','budget_exhausted')),
    CHECK ((outcome='preview' AND applied_at IS NULL AND removed_count IS NULL) OR
           (outcome<>'preview' AND applied_at IS NOT NULL AND removed_count IS NOT NULL)),
    PRIMARY KEY(grant_id,batch_no)
);
CREATE INDEX idx_origin_retention_app ON mobile_origin_uses(application_public_id,expires_at,issuer,nonce_digest);
CREATE INDEX idx_receipt_retention_app ON notification_receipt_requests(application_id,expires_unix,user_id,client_event_id);

-- A single autocommit call is one batch. Never grant a maintenance login table
-- mutation rights. The effective owner needs only the privileges in the runbook.
CREATE FUNCTION run_retention_batch(
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
REVOKE ALL ON FUNCTION run_retention_batch(uuid,uuid,uuid,uuid,text,integer,boolean) FROM PUBLIC;
