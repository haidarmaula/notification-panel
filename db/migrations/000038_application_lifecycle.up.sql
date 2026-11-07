-- Offline owner installation later expands the registry status constraint.
-- New projects and freshly migrated databases remain provisioning-only.
CREATE TABLE application_lifecycle_grants (
    id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
    project_id uuid NOT NULL REFERENCES applications(public_id) ON DELETE RESTRICT,
    operator_role oid NOT NULL,
    issuer_role oid NOT NULL DEFAULT (session_user::regrole::oid),
    origin_epoch uuid NOT NULL,
    source_epoch uuid NOT NULL,
    expected_revision bigint NOT NULL CHECK (expected_revision BETWEEN 1 AND 9223372036854775806),
    previous_status text NOT NULL CHECK (previous_status IN ('PROVISIONING','ACTIVE','HELD')),
    destination text NOT NULL CHECK (destination IN ('ACTIVE','HELD','RETIRED') AND destination <> previous_status),
    ownership_evidence bytea NOT NULL CHECK (octet_length(ownership_evidence)=32),
    writer_fence_evidence bytea NOT NULL CHECK (octet_length(writer_fence_evidence)=32),
    cohort_fence_evidence bytea NOT NULL CHECK (octet_length(cohort_fence_evidence)=32),
    retired_writer_roles name[] NOT NULL CHECK (cardinality(retired_writer_roles)<=64 AND array_position(retired_writer_roles,NULL) IS NULL),
    issued_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    applied_at timestamptz,
    CHECK (operator_role<>issuer_role),
    CHECK (isfinite(issued_at) AND isfinite(expires_at) AND expires_at>issued_at AND expires_at<=issued_at+interval '24 hours'),
    CHECK (applied_at IS NULL OR (applied_at>=issued_at AND applied_at<expires_at AND revoked_at IS NULL)),
    CHECK (revoked_at IS NULL OR (isfinite(revoked_at) AND revoked_at>=issued_at))
);
CREATE UNIQUE INDEX application_lifecycle_applied_revision
    ON application_lifecycle_grants(project_id,expected_revision) WHERE applied_at IS NOT NULL;

CREATE FUNCTION protect_application_lifecycle_grant() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF TG_OP<>'UPDATE' OR current_user::regrole::oid<>(SELECT relowner FROM pg_class WHERE oid=TG_RELID) OR OLD.applied_at IS NOT NULL OR OLD.revoked_at IS NOT NULL OR
       (to_jsonb(OLD)-'applied_at'-'revoked_at') IS DISTINCT FROM (to_jsonb(NEW)-'applied_at'-'revoked_at')
    THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='immutable application lifecycle evidence'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER protect_application_lifecycle_grant BEFORE UPDATE OR DELETE ON application_lifecycle_grants
    FOR EACH ROW EXECUTE FUNCTION protect_application_lifecycle_grant();
CREATE TRIGGER protect_application_lifecycle_history BEFORE TRUNCATE ON application_lifecycle_grants
    FOR EACH STATEMENT EXECUTE FUNCTION protect_application_lifecycle_grant();

-- Bind issuance to distinct dedicated database logins, even if someone
-- accidentally grants a service login INSERT on this table.
CREATE FUNCTION validate_application_lifecycle_issuer() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE issuer_group oid := to_regrole('hello_application_lifecycle_issuer');
        operator_group oid := to_regrole('hello_application_lifecycle_operator');
BEGIN
    IF NEW.applied_at IS NOT NULL OR NEW.revoked_at IS NOT NULL OR issuer_group IS NULL OR operator_group IS NULL OR NEW.issuer_role<>session_user::regrole::oid
       OR NOT pg_has_role(NEW.issuer_role,issuer_group,'USAGE') OR pg_has_role(NEW.issuer_role,operator_group,'MEMBER')
       OR NOT EXISTS (SELECT FROM pg_roles actor WHERE actor.oid=NEW.operator_role AND actor.rolcanlogin AND NOT actor.rolsuper
             AND NOT actor.rolcreaterole AND NOT actor.rolreplication AND NOT actor.rolbypassrls
             AND pg_has_role(actor.oid,operator_group,'USAGE') AND NOT pg_has_role(actor.oid,issuer_group,'MEMBER'))
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='distinct lifecycle issuer and executor required'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER validate_application_lifecycle_issuer BEFORE INSERT ON application_lifecycle_grants
    FOR EACH ROW EXECUTE FUNCTION validate_application_lifecycle_issuer();

-- The original ownership finalizer validates all eleven business tables.
CREATE FUNCTION application_ownership_finalized() RETURNS boolean
LANGUAGE sql STABLE SET search_path=pg_catalog AS $$
    SELECT count(*)=11 FROM unnest(ARRAY[
        'public.users'::regclass,'public.device_tokens'::regclass,'public.templates'::regclass,
        'public.segments'::regclass,'public.segment_members'::regclass,'public.notifications'::regclass,
        'public.upload_batches'::regclass,'public.upload_batch_rows'::regclass,'public.notification_targets'::regclass,
        'public.notification_deliveries'::regclass,'public.notification_reads'::regclass
    ]) owned(rel) JOIN pg_attribute a ON a.attrelid=owned.rel
    WHERE a.attname='application_id' AND a.attnotnull AND NOT a.attisdropped
$$;
REVOKE ALL ON FUNCTION application_ownership_finalized() FROM PUBLIC;

-- The owner installs this trigger only during the explicit offline cutover.
CREATE FUNCTION guard_application_lifecycle() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    IF TG_OP='INSERT' THEN
        IF NEW.status<>'PROVISIONING' OR NEW.revision<>1 THEN
            RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='new application must be provisioning';
        END IF;
    ELSIF (NEW.id,NEW.public_id,NEW.key,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.public_id,OLD.key,OLD.created_at) THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='immutable application identity';
    ELSIF NEW.status IS DISTINCT FROM OLD.status THEN
        IF current_user::regrole::oid<>(SELECT relowner FROM pg_class WHERE oid=TG_RELID) THEN
            RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='unapproved application transition';
        END IF;
        IF NEW.revision<>OLD.revision+1 OR NOT EXISTS (
            SELECT 1 FROM public.application_lifecycle_grants g
            WHERE g.project_id=OLD.public_id AND g.expected_revision=OLD.revision AND g.previous_status=OLD.status AND g.destination=NEW.status
              AND g.applied_at IS NOT NULL AND g.operator_role=session_user::regrole::oid
        ) THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='unapproved application transition'; END IF;
    ELSIF NEW.revision<OLD.revision THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='application revision may not go backwards';
    END IF;
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION guard_application_lifecycle() FROM PUBLIC;

CREATE FUNCTION revoke_application_lifecycle(p_grant uuid, p_project uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE g public.application_lifecycle_grants%ROWTYPE;
BEGIN
    SELECT * INTO g FROM public.application_lifecycle_grants WHERE id=p_grant AND project_id=p_project
      AND issuer_role=session_user::regrole::oid FOR UPDATE;
    IF NOT FOUND OR g.applied_at IS NOT NULL THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='lifecycle approval unavailable'; END IF;
    IF g.revoked_at IS NULL THEN UPDATE public.application_lifecycle_grants SET revoked_at=clock_timestamp() WHERE id=g.id; END IF;
END;
$$;
REVOKE ALL ON FUNCTION revoke_application_lifecycle(uuid,uuid) FROM PUBLIC;

CREATE FUNCTION apply_application_lifecycle(p_grant uuid,p_project uuid) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE
    g public.application_lifecycle_grants%ROWTYPE;
    app public.applications%ROWTYPE;
    policy public.application_dispatch_policy%ROWTYPE;
    origin uuid;
    source uuid;
BEGIN
    IF current_setting('transaction_isolation')<>'read committed' OR pg_has_role(session_user,current_user,'MEMBER')
       OR pg_has_role(session_user,to_regrole('hello_application_lifecycle_operator'),'USAGE') IS DISTINCT FROM true
       OR pg_has_role(session_user,to_regrole('hello_application_lifecycle_issuer'),'MEMBER') IS DISTINCT FROM false
       OR NOT EXISTS (SELECT FROM public.application_lifecycle_grants WHERE id=p_grant AND project_id=p_project AND operator_role=session_user::regrole::oid)
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='lifecycle approval unavailable'; END IF;

    SELECT origin_epoch,source_epoch INTO STRICT origin,source FROM public.notification_runtime WHERE singleton FOR SHARE;
    SELECT * INTO STRICT app FROM public.applications WHERE public_id=p_project FOR NO KEY UPDATE;
    SELECT * INTO STRICT g FROM public.application_lifecycle_grants WHERE id=p_grant AND project_id=p_project FOR UPDATE;
    IF (g.origin_epoch,g.source_epoch) IS DISTINCT FROM (origin,source) OR g.revoked_at IS NOT NULL THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='lifecycle approval unavailable';
    END IF;
    -- An exact retry returns its original committed revision, even after later transitions.
    IF g.applied_at IS NOT NULL THEN RETURN g.expected_revision+1; END IF;
    IF g.issued_at>clock_timestamp() OR g.expires_at<=clock_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='lifecycle approval expired';
    END IF;
    IF (app.status,app.revision) IS DISTINCT FROM (g.previous_status,g.expected_revision) OR app.status='RETIRED' THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='application revision conflict';
    END IF;
    IF NOT EXISTS (SELECT FROM pg_trigger WHERE tgrelid='public.applications'::regclass AND tgname='guard_application_lifecycle'
        AND tgfoid='public.guard_application_lifecycle()'::regprocedure AND tgenabled IN ('O','A')) THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='offline lifecycle installation required';
    END IF;
    SELECT * INTO policy FROM public.application_dispatch_policy WHERE application_id=app.id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='application dispatch policy required'; END IF;
    IF g.destination='ACTIVE' THEN
        -- Keep the checked catalog constraints stable through commit.
        LOCK TABLE public.users,public.device_tokens,public.templates,public.segments,public.segment_members,public.notifications,
            public.upload_batches,public.upload_batch_rows,public.notification_targets,public.notification_deliveries,public.notification_reads IN ACCESS SHARE MODE;
        IF NOT public.application_ownership_finalized() OR NOT policy.hold_new OR policy.coverage_approved
           OR EXISTS (SELECT FROM public.current_binding_routes r WHERE r.application_id=app.id AND r.destination='FCM_ACTIVE'
               AND r.origin_epoch=origin AND r.source_epoch=source)
           OR EXISTS (SELECT FROM pg_roles retired WHERE retired.rolname=ANY(g.retired_writer_roles)
               AND (retired.rolcanlogin OR EXISTS (SELECT FROM pg_roles member WHERE member.rolcanlogin AND NOT member.rolsuper
                   AND pg_has_role(member.oid,retired.oid,'MEMBER'))))
           OR EXISTS (SELECT FROM pg_stat_activity conn WHERE conn.datname=current_database() AND conn.usename=ANY(g.retired_writer_roles))
        THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='application enrollment remains fenced'; END IF;
    ELSE
        UPDATE public.application_dispatch_policy SET hold_new=true,coverage_approved=false WHERE application_id=app.id;
    END IF;
    UPDATE public.application_lifecycle_grants SET applied_at=clock_timestamp() WHERE id=g.id;
    UPDATE public.applications SET status=g.destination,revision=revision+1,updated_at=clock_timestamp() WHERE id=app.id;
    RETURN app.revision+1;
END;
$$;
REVOKE ALL ON FUNCTION apply_application_lifecycle(uuid,uuid) FROM PUBLIC;
