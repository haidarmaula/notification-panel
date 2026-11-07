-- Grants are issued by a restricted control-plane identity after checking the
-- cohort, legacy exclusion and target evidence. No operator or route is seeded.
CREATE TABLE binding_route_grants (
    id uuid PRIMARY KEY,
    application_id bigint NOT NULL,
    binding_id bigint NOT NULL,
    operator_role oid NOT NULL,
    issuer_role oid NOT NULL DEFAULT session_user::regrole::oid,
    origin_epoch uuid NOT NULL,
    source_epoch uuid NOT NULL,
    expected_revision bigint NOT NULL CHECK (expected_revision BETWEEN 0 AND 9223372036854775806),
    destination text NOT NULL CHECK (destination IN ('FCM_ACTIVE','HELD','RETIRED')),
    cohort_id uuid NOT NULL,
    evidence_id uuid NOT NULL,
    reason text NOT NULL CHECK (reason IN ('pilot','expansion','incident','rollback','retirement','revalidation')),
    resume_from bigint CHECK ((resume_from IS NULL AND reason<>'revalidation') OR (resume_from IS NOT NULL AND resume_from > 0 AND resume_from <= expected_revision AND destination='FCM_ACTIVE' AND reason='revalidation')),
    issued_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    applied_at timestamptz,
    FOREIGN KEY (application_id,binding_id) REFERENCES installation_bindings(application_id,id) ON DELETE RESTRICT,
    CHECK (issuer_role<>operator_role),
    CHECK (isfinite(issued_at) AND isfinite(expires_at) AND expires_at>issued_at),
    CHECK ((destination='FCM_ACTIVE' AND reason IN ('pilot','expansion','revalidation')) OR
           (destination='HELD' AND reason IN ('incident','rollback')) OR
           (destination='RETIRED' AND reason='retirement')),
    CHECK (applied_at IS NULL OR (applied_at>=issued_at AND applied_at<expires_at AND revoked_at IS NULL)),
    CHECK (revoked_at IS NULL OR revoked_at>=issued_at)
);
CREATE UNIQUE INDEX uq_binding_route_revision ON binding_route_grants(application_id,binding_id,expected_revision DESC) WHERE applied_at IS NOT NULL;
CREATE VIEW current_binding_routes AS
SELECT DISTINCT ON (application_id,binding_id) application_id,binding_id,expected_revision+1 AS revision,
       destination,cohort_id,resume_from,origin_epoch,source_epoch
FROM binding_route_grants WHERE applied_at IS NOT NULL ORDER BY application_id,binding_id,expected_revision DESC;

CREATE FUNCTION guard_binding_route_grant() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF TG_OP='INSERT' THEN
        IF (NEW.issuer_role<>session_user::regrole::oid OR NEW.applied_at IS NOT NULL) AND
           current_user::regrole::oid<>(SELECT relowner FROM pg_class WHERE oid=TG_RELID) THEN
            RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='route issuance unavailable';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP='DELETE' OR OLD.applied_at IS NOT NULL OR OLD.revoked_at IS NOT NULL OR
       (to_jsonb(NEW)-'revoked_at'-'applied_at') IS DISTINCT FROM (to_jsonb(OLD)-'revoked_at'-'applied_at') THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='immutable route approval';
    END IF;
    IF (NEW.applied_at IS NOT NULL AND (OLD.operator_role<>session_user::regrole::oid OR
        current_user::regrole::oid<>(SELECT relowner FROM pg_class WHERE oid=TG_RELID))) OR
       (NEW.revoked_at IS NOT NULL AND OLD.issuer_role<>session_user::regrole::oid) THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='route approval unavailable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER guard_binding_route_grant BEFORE INSERT OR UPDATE OR DELETE ON binding_route_grants FOR EACH ROW EXECUTE FUNCTION guard_binding_route_grant();

ALTER TABLE notification_delivery_intents ADD COLUMN audience_route_revision bigint CHECK (audience_route_revision>0);
CREATE FUNCTION guard_audience_route_revision() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF NEW.audience_route_revision IS DISTINCT FROM OLD.audience_route_revision THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='immutable audience route revision';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER guard_audience_route_revision BEFORE UPDATE OF audience_route_revision ON notification_delivery_intents FOR EACH ROW EXECUTE FUNCTION guard_audience_route_revision();

-- The login can apply only its exact pre-authorized action. Holding the app
-- row orders this change against acceptance, enrollment and worker preflight.
CREATE FUNCTION apply_binding_route(p_grant uuid,p_project uuid) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='1s' AS $$
DECLARE
    g public.binding_route_grants%ROWTYPE;
    prev public.current_binding_routes%ROWTYPE;
    app_id bigint;
    project_status text;
    o uuid;
    s uuid;
    fenced boolean;
BEGIN
    IF current_setting('transaction_isolation')<>'read committed' OR pg_has_role(session_user,current_user,'MEMBER') THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='route approval unavailable';
    END IF;
    SELECT a.id INTO app_id FROM public.binding_route_grants j JOIN public.applications a ON a.id=j.application_id
        WHERE j.id=p_grant AND a.public_id=p_project AND j.operator_role=session_user::regrole::oid;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='route approval unavailable'; END IF;
    SELECT origin_epoch,source_epoch,sealed OR migration_hold INTO STRICT o,s,fenced FROM public.notification_runtime WHERE singleton FOR SHARE;
    SELECT status INTO STRICT project_status FROM public.applications WHERE id=app_id FOR NO KEY UPDATE;
    SELECT * INTO STRICT g FROM public.binding_route_grants WHERE id=p_grant AND application_id=app_id FOR UPDATE;
    IF g.origin_epoch<>o OR g.source_epoch<>s OR g.revoked_at IS NOT NULL THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='route approval unavailable';
    END IF;
    IF g.applied_at IS NOT NULL THEN RETURN g.expected_revision+1; END IF;
    IF g.issued_at>clock_timestamp() OR g.expires_at<=clock_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='route approval expired';
    END IF;
    SELECT * INTO prev FROM public.current_binding_routes WHERE application_id=app_id AND binding_id=g.binding_id;
    IF coalesce(prev.revision,0)<>g.expected_revision OR prev.destination='RETIRED' THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='route revision conflict';
    END IF;
    IF g.destination='FCM_ACTIVE' THEN
        IF fenced OR project_status<>'ACTIVE' OR NOT EXISTS (
            SELECT 1 FROM public.installation_bindings b JOIN public.mobile_installations i ON (i.application_id,i.id)=(b.application_id,b.installation_id)
            JOIN public.users u ON (u.application_id,u.id)=(b.application_id,b.owner_user_id)
            WHERE b.application_id=app_id AND b.id=g.binding_id AND b.retired_at IS NULL AND u.status='ACTIVE'
                AND (i.owner_user_id,i.owner_epoch)=(b.owner_user_id,b.owner_epoch)
        ) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='binding activation held'; END IF;
        IF g.resume_from IS NOT NULL AND NOT EXISTS (
            SELECT 1 FROM public.binding_route_grants old WHERE old.application_id=app_id AND old.binding_id=g.binding_id
                AND old.applied_at IS NOT NULL AND old.expected_revision=g.resume_from-1 AND old.destination='FCM_ACTIVE'
                AND old.cohort_id=g.cohort_id AND old.origin_epoch=o AND old.source_epoch=s
        ) THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='invalid route revalidation'; END IF;
    END IF;
    UPDATE public.binding_route_grants SET applied_at=clock_timestamp() WHERE id=g.id;
    RETURN g.expected_revision+1;
END;
$$;
REVOKE ALL ON FUNCTION apply_binding_route(uuid,uuid) FROM PUBLIC;
