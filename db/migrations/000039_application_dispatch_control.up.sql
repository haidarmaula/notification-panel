-- The expanded M38 constraint is durable across a disabled trigger. Isolated
-- pre-cutover fixtures have neither marker; the original production registry
-- constraint permits PROVISIONING only.
CREATE FUNCTION application_lifecycle_managed() RETURNS boolean
LANGUAGE sql STABLE SET search_path=pg_catalog AS $$
    SELECT EXISTS(SELECT FROM pg_constraint WHERE conrelid='public.applications'::regclass AND conname='application_lifecycle_status')
        OR EXISTS(SELECT FROM pg_trigger WHERE tgrelid='public.applications'::regclass AND tgname='guard_application_lifecycle')
$$;

ALTER TABLE application_dispatch_policy
    ADD COLUMN dispatch_revision bigint NOT NULL DEFAULT 1 CHECK(dispatch_revision>0),
    ADD COLUMN dispatch_mode text NOT NULL DEFAULT 'HELD' CHECK(dispatch_mode IN ('HELD','PILOT','FULL')),
    ADD COLUMN dispatch_cohort uuid,
    ADD COLUMN dispatch_origin_epoch uuid,
    ADD COLUMN dispatch_source_epoch uuid,
    ADD CONSTRAINT dispatch_release_shape CHECK (
        (dispatch_mode='HELD' AND dispatch_cohort IS NULL AND dispatch_origin_epoch IS NULL AND dispatch_source_epoch IS NULL)
        OR (dispatch_mode IN ('PILOT','FULL') AND (dispatch_cohort IS NOT NULL)=(dispatch_mode='PILOT')
            AND dispatch_origin_epoch IS NOT NULL AND dispatch_source_epoch IS NOT NULL AND NOT hold_new AND coverage_approved=(dispatch_mode='FULL'))
    );
-- A previous owner-installed deployment must acquire a fresh release.
UPDATE application_dispatch_policy SET hold_new=true,coverage_approved=false WHERE public.application_lifecycle_managed();

-- M23's whole-row immutability trigger also protects this field.
ALTER TABLE notification_preparations ADD COLUMN prepared_pilot_cohort uuid;

CREATE TABLE application_dispatch_grants (
    id uuid PRIMARY KEY CHECK(id<>'00000000-0000-0000-0000-000000000000'),
    project_id uuid NOT NULL REFERENCES applications(public_id) ON DELETE RESTRICT,
    operator_role oid NOT NULL,
    issuer_role oid NOT NULL DEFAULT (session_user::regrole::oid) CHECK(issuer_role<>operator_role),
    application_revision bigint NOT NULL CHECK(application_revision>0),
    expected_revision bigint NOT NULL CHECK(expected_revision BETWEEN 1 AND 9223372036854775806),
    previous_mode text NOT NULL CHECK(previous_mode IN ('HELD','PILOT','FULL')),
    destination text NOT NULL CHECK(destination IN ('HELD','PILOT','FULL')),
    cohort_id uuid CHECK(cohort_id<>'00000000-0000-0000-0000-000000000000'),
    origin_epoch uuid NOT NULL,
    source_epoch uuid NOT NULL,
    reason text NOT NULL,
    legacy_evidence bytea NOT NULL CHECK(octet_length(legacy_evidence)=32),
    consent_evidence bytea NOT NULL CHECK(octet_length(consent_evidence)=32),
    coverage_evidence bytea NOT NULL CHECK(octet_length(coverage_evidence)=32),
    quota_evidence bytea NOT NULL CHECK(octet_length(quota_evidence)=32),
    issued_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    applied_at timestamptz,
    CHECK((destination='HELD' AND cohort_id IS NULL AND reason IN ('incident','rollback'))
       OR (destination='PILOT' AND cohort_id IS NOT NULL AND reason IN ('pilot','revalidation'))
       OR (destination='FULL' AND previous_mode='PILOT' AND cohort_id IS NOT NULL AND reason='full_coverage')),
    CHECK(isfinite(issued_at) AND isfinite(expires_at) AND expires_at>issued_at AND expires_at<=issued_at+interval '24 hours'),
    CHECK(applied_at IS NULL OR (applied_at>=issued_at AND applied_at<expires_at AND revoked_at IS NULL)),
    CHECK(revoked_at IS NULL OR (isfinite(revoked_at) AND revoked_at>=issued_at))
);
CREATE UNIQUE INDEX uq_application_dispatch_revision
    ON application_dispatch_grants(project_id,expected_revision) WHERE applied_at IS NOT NULL;

-- Recheck the actual login and inherited authority at issuance and use. Role
-- changes after a grant was issued must not widen its effective authority.
CREATE FUNCTION application_dispatch_actor_valid(actor oid,is_issuer boolean) RETURNS boolean
LANGUAGE sql STABLE SET search_path=pg_catalog AS $$
    SELECT EXISTS (
        SELECT FROM pg_roles a WHERE a.oid=actor AND a.rolcanlogin AND NOT a.rolsuper AND NOT a.rolcreatedb
          AND NOT a.rolcreaterole AND NOT a.rolreplication AND NOT a.rolbypassrls
          AND pg_has_role(a.oid,to_regrole(CASE WHEN is_issuer THEN 'hello_dispatch_issuer' ELSE 'hello_dispatch_operator' END),'USAGE')
          AND NOT pg_has_role(a.oid,to_regrole(CASE WHEN is_issuer THEN 'hello_dispatch_operator' ELSE 'hello_dispatch_issuer' END),'MEMBER')
          AND NOT pg_has_role(a.oid,(SELECT relowner FROM pg_class WHERE oid='public.application_dispatch_grants'::regclass),'MEMBER')
          AND NOT has_database_privilege(a.oid,current_database(),'CREATE') AND NOT has_schema_privilege(a.oid,'public','CREATE')
          AND NOT EXISTS (SELECT FROM unnest(ARRAY['public.notification_runtime'::regclass,'public.applications'::regclass,
              'public.application_dispatch_policy'::regclass,'public.binding_route_grants'::regclass]) protected(rel)
              WHERE has_any_column_privilege(a.oid,protected.rel,'INSERT,UPDATE,REFERENCES')
                 OR has_table_privilege(a.oid,protected.rel,'DELETE,TRUNCATE,TRIGGER,MAINTAIN'))
          AND NOT has_table_privilege(a.oid,'public.application_dispatch_grants','INSERT,DELETE,TRUNCATE,TRIGGER,MAINTAIN')
          AND NOT has_any_column_privilege(a.oid,'public.application_dispatch_grants','UPDATE,REFERENCES')
          AND (is_issuer OR NOT has_any_column_privilege(a.oid,'public.application_dispatch_grants','INSERT'))
          AND NOT EXISTS (SELECT FROM unnest(ARRAY['issued_at','issuer_role','applied_at','revoked_at']) cols(col)
              WHERE has_column_privilege(a.oid,'public.application_dispatch_grants',cols.col,'INSERT'))
    )
$$;

CREATE FUNCTION protect_application_dispatch_grant() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF TG_OP='INSERT' THEN
        IF NEW.applied_at IS NOT NULL OR NEW.revoked_at IS NOT NULL OR NEW.issuer_role<>session_user::regrole::oid
           OR NOT public.application_dispatch_actor_valid(NEW.issuer_role,true)
           OR NOT public.application_dispatch_actor_valid(NEW.operator_role,false)
        THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='distinct dispatch reviewer and executor required'; END IF;
        RETURN NEW;
    END IF;
    IF TG_OP<>'UPDATE' OR OLD.applied_at IS NOT NULL OR OLD.revoked_at IS NOT NULL
       OR current_user::regrole::oid<>(SELECT relowner FROM pg_class WHERE oid=TG_RELID)
       OR (to_jsonb(OLD)-'applied_at'-'revoked_at') IS DISTINCT FROM (to_jsonb(NEW)-'applied_at'-'revoked_at')
       OR (NEW.applied_at IS NOT NULL AND OLD.operator_role<>session_user::regrole::oid)
       OR (NEW.revoked_at IS NOT NULL AND OLD.issuer_role<>session_user::regrole::oid)
    THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='immutable dispatch approval'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER protect_application_dispatch_grant BEFORE INSERT OR UPDATE OR DELETE ON application_dispatch_grants
    FOR EACH ROW EXECUTE FUNCTION protect_application_dispatch_grant();
CREATE TRIGGER protect_application_dispatch_history BEFORE TRUNCATE ON application_dispatch_grants
    FOR EACH STATEMENT EXECUTE FUNCTION protect_application_dispatch_grant();

CREATE FUNCTION guard_application_dispatch_policy() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF TG_OP IN ('DELETE','TRUNCATE') THEN
        IF public.application_lifecycle_managed() THEN
            RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='managed dispatch generation cannot be reset';
        END IF;
        RETURN OLD;
    END IF;
    IF NOT public.application_lifecycle_managed() THEN
        -- Even prior to the offline cutover there is no release authority to pre-seed.
        IF NEW.dispatch_revision<>1 OR NEW.dispatch_mode<>'HELD' THEN
            RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='offline lifecycle installation required';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP='INSERT' THEN
        IF NOT NEW.hold_new OR NEW.coverage_approved OR NEW.dispatch_mode<>'HELD' OR NEW.dispatch_revision<>1 THEN
            RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='new dispatch policy must be held';
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.hold_new THEN
        -- A provider's one-way hold and M38's project hold use this path too.
        -- Every hold invalidates every unconsumed grant for the previous generation.
        NEW.coverage_approved:=false;
        NEW.dispatch_mode:='HELD';
        NEW.dispatch_cohort:=NULL;
        NEW.dispatch_origin_epoch:=NULL;
        NEW.dispatch_source_epoch:=NULL;
        NEW.dispatch_revision:=OLD.dispatch_revision+1;
        RETURN NEW;
    END IF;
    IF current_user::regrole::oid<>(SELECT relowner FROM pg_class WHERE oid=TG_RELID) THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='dispatch policy release unavailable';
    END IF;
    IF NEW.dispatch_revision<>OLD.dispatch_revision+1 OR NOT EXISTS (
        SELECT FROM public.application_dispatch_grants g JOIN public.applications a ON a.public_id=g.project_id
        WHERE a.id=OLD.application_id AND g.applied_at IS NOT NULL AND g.expected_revision=OLD.dispatch_revision
          AND g.previous_mode=OLD.dispatch_mode AND g.destination=NEW.dispatch_mode AND g.operator_role=session_user::regrole::oid
          AND (g.origin_epoch,g.source_epoch)=(NEW.dispatch_origin_epoch,NEW.dispatch_source_epoch)
          AND (NEW.dispatch_mode='FULL' OR NEW.dispatch_cohort=g.cohort_id)
    ) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='dispatch policy release unavailable'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER guard_application_dispatch_policy BEFORE INSERT OR UPDATE OF hold_new,coverage_approved,dispatch_revision,dispatch_mode,
    dispatch_cohort,dispatch_origin_epoch,dispatch_source_epoch ON application_dispatch_policy
    FOR EACH ROW EXECUTE FUNCTION guard_application_dispatch_policy();

CREATE TRIGGER guard_application_dispatch_delete BEFORE DELETE ON application_dispatch_policy
    FOR EACH ROW EXECUTE FUNCTION guard_application_dispatch_policy();
CREATE TRIGGER guard_application_dispatch_truncate BEFORE TRUNCATE ON application_dispatch_policy
    FOR EACH STATEMENT EXECUTE FUNCTION guard_application_dispatch_policy();

CREATE FUNCTION revoke_application_dispatch(p_grant uuid,p_project uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE g public.application_dispatch_grants%ROWTYPE;
BEGIN
    SELECT * INTO g FROM public.application_dispatch_grants WHERE id=p_grant AND project_id=p_project AND issuer_role=session_user::regrole::oid FOR UPDATE;
    IF NOT FOUND OR g.applied_at IS NOT NULL THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='dispatch approval unavailable'; END IF;
    IF g.revoked_at IS NULL THEN UPDATE public.application_dispatch_grants SET revoked_at=clock_timestamp() WHERE id=g.id; END IF;
END;
$$;
REVOKE ALL ON FUNCTION revoke_application_dispatch(uuid,uuid) FROM PUBLIC;

CREATE FUNCTION apply_application_dispatch(p_grant uuid,p_project uuid) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE
    g public.application_dispatch_grants%ROWTYPE;
    app public.applications%ROWTYPE;
    policy public.application_dispatch_policy%ROWTYPE;
    runtime public.notification_runtime%ROWTYPE;
BEGIN
    IF current_setting('transaction_isolation')<>'read committed' OR pg_has_role(session_user,current_user,'MEMBER')
       OR pg_has_role(session_user,to_regrole('hello_dispatch_operator'),'USAGE') IS DISTINCT FROM true
       OR pg_has_role(session_user,to_regrole('hello_dispatch_issuer'),'MEMBER') IS DISTINCT FROM false
       OR NOT EXISTS(SELECT FROM public.application_dispatch_grants WHERE id=p_grant AND project_id=p_project AND operator_role=session_user::regrole::oid)
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='dispatch approval unavailable'; END IF;
    SELECT * INTO STRICT runtime FROM public.notification_runtime WHERE singleton FOR SHARE;
    SELECT * INTO STRICT app FROM public.applications WHERE public_id=p_project FOR NO KEY UPDATE;
    SELECT * INTO STRICT policy FROM public.application_dispatch_policy WHERE application_id=app.id FOR UPDATE;
    SELECT * INTO STRICT g FROM public.application_dispatch_grants WHERE id=p_grant FOR UPDATE;
    IF NOT public.application_dispatch_actor_valid(g.issuer_role,true) OR NOT public.application_dispatch_actor_valid(g.operator_role,false)
       OR (g.origin_epoch,g.source_epoch) IS DISTINCT FROM (runtime.origin_epoch,runtime.source_epoch) OR g.revoked_at IS NOT NULL THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='dispatch approval unavailable';
    END IF;
    IF g.applied_at IS NOT NULL THEN RETURN g.expected_revision+1; END IF;
    IF g.issued_at>clock_timestamp() OR g.expires_at<=clock_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='dispatch approval expired';
    END IF;
    IF (g.application_revision,g.expected_revision,g.previous_mode) IS DISTINCT FROM (app.revision,policy.dispatch_revision,policy.dispatch_mode) THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='dispatch revision conflict';
    END IF;
    IF NOT EXISTS(SELECT FROM pg_trigger WHERE tgrelid='public.applications'::regclass AND tgname='guard_application_lifecycle'
        AND tgfoid='public.guard_application_lifecycle()'::regprocedure AND tgenabled IN ('O','A')) THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='offline lifecycle installation required';
    END IF;
    IF g.destination<>'HELD' THEN
        IF runtime.sealed OR runtime.migration_hold OR app.status<>'ACTIVE' OR NOT public.application_ownership_finalized()
           OR (g.destination='FULL' AND (policy.dispatch_cohort,policy.dispatch_origin_epoch,policy.dispatch_source_epoch)
              IS DISTINCT FROM (g.cohort_id,g.origin_epoch,g.source_epoch))
           OR NOT EXISTS(
               SELECT FROM public.current_binding_routes r
               JOIN public.installation_bindings b ON (b.application_id,b.id)=(r.application_id,r.binding_id)
               JOIN public.mobile_installations i ON (i.application_id,i.id,i.owner_user_id,i.owner_epoch)=(b.application_id,b.installation_id,b.owner_user_id,b.owner_epoch)
               JOIN public.users u ON (u.application_id,u.id)=(b.application_id,b.owner_user_id)
               WHERE r.application_id=app.id AND r.cohort_id=g.cohort_id AND r.destination='FCM_ACTIVE'
                 AND (r.origin_epoch,r.source_epoch)=(g.origin_epoch,g.source_epoch) AND b.retired_at IS NULL AND u.status='ACTIVE'
           )
        THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='dispatch release remains fenced'; END IF;
    END IF;
    UPDATE public.application_dispatch_grants SET applied_at=clock_timestamp() WHERE id=g.id;
    UPDATE public.application_dispatch_policy SET hold_new=(g.destination='HELD'),coverage_approved=(g.destination='FULL'),
        dispatch_revision=dispatch_revision+1,dispatch_mode=g.destination,dispatch_cohort=CASE WHEN g.destination='PILOT' THEN g.cohort_id END,
        dispatch_origin_epoch=CASE WHEN g.destination<>'HELD' THEN g.origin_epoch END,dispatch_source_epoch=CASE WHEN g.destination<>'HELD' THEN g.source_epoch END
    WHERE application_id=app.id;
    RETURN g.expected_revision+1;
END;
$$;
REVOKE ALL ON FUNCTION apply_application_dispatch(uuid,uuid) FROM PUBLIC;
