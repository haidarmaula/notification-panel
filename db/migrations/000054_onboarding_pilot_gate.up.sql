-- M53's live-target view is for pending/running work. Release must instead
-- match the completed operation, its evidence, and the current dispatch roster.
CREATE FUNCTION onboarding_pilot_ready(p_application bigint,p_cohort uuid)
RETURNS boolean LANGUAGE sql SET search_path=pg_catalog,pg_temp AS $$
    WITH checked_at AS MATERIALIZED (SELECT clock_timestamp() AS at)
    SELECT EXISTS (
        SELECT FROM public.application_onboarding s
        JOIN public.applications a ON a.id=s.application_id AND a.status='ACTIVE'
        JOIN public.application_dispatch_policy d ON d.application_id=a.id
        JOIN public.application_onboarding_operations o ON (o.application_id,o.id)=(a.id,s.cohort_id)
        JOIN public.application_onboarding_approvals p ON (p.application_id,p.id)=(a.id,o.approval_id)
        JOIN public.application_provider_configurations c ON (c.application_id,c.id)=(a.id,s.current_configuration_id)
        JOIN public.application_provider_checks e ON (e.application_id,e.operation_id,e.configuration_id)=(a.id,o.id,c.id)
        JOIN public.application_runtime_capacity_profile f ON f.environment=s.environment
        CROSS JOIN public.notification_runtime r
        CROSS JOIN checked_at t
        WHERE a.id=p_application AND s.cohort_id=p_cohort AND r.singleton AND NOT r.sealed AND NOT r.migration_hold
          AND c.environment=s.environment AND c.state='VERIFIED' AND c.file_reference=c.id
          AND s.enrollment_profile_id IS NOT NULL AND jsonb_array_length(s.enrollment_platforms)>0
          AND o.kind='TEST_COHORT' AND o.state='SUCCEEDED' AND o.completed_by IS NOT NULL
          AND o.resulting_revision=o.expected_setup_revision+1 AND o.resulting_revision<=s.revision
          AND o.configuration_id=c.id AND (o.cohort_id IS NULL OR o.cohort_id=o.id)
          AND (o.expected_application_revision,o.expected_dispatch_revision)=(a.revision,d.dispatch_revision)
          AND (o.origin_epoch,o.source_epoch)=(r.origin_epoch,r.source_epoch)
          AND p.kind='TEST_COHORT' AND p.configuration_id=c.id AND p.profile_revision=f.revision
          AND (p.cohort_id IS NULL OR p.cohort_id=o.id)
          AND (p.origin_epoch,p.source_epoch)=(r.origin_epoch,r.source_epoch)
          AND (p.application_revision,p.dispatch_revision)=(a.revision,d.dispatch_revision)
          AND p.revoked_at IS NULL AND p.consumed_at BETWEEN p.issued_at AND o.completed_at AND p.expires_at>t.at
          AND e.kind='TARGET_PERMISSION' AND e.outcome='PASSED' AND e.binding_id IS NULL AND e.route_revision IS NULL
          AND (e.origin_epoch,e.source_epoch)=(r.origin_epoch,r.source_epoch)
          AND e.checked_at=o.completed_at
          AND e.checked_at BETWEEN t.at-interval '15 minutes' AND t.at AND e.expires_at>t.at
          AND cardinality(o.installation_ids) BETWEEN 1 AND 10
          AND ARRAY(SELECT member FROM unnest(o.installation_ids) AS ids(member) ORDER BY member) = ARRAY(
              SELECT i.public_id FROM public.application_onboarding_approved_targets m
              JOIN public.mobile_installations i ON (i.application_id,i.id)=(a.id,m.installation_id)
              WHERE (m.application_id,m.approval_id)=(a.id,p.id) ORDER BY i.public_id)
          AND cardinality(o.installation_ids)=(SELECT count(*) FROM public.application_onboarding_cohort_targets x WHERE x.operation_id=o.id)
          AND cardinality(o.installation_ids)=(
              SELECT count(*) FROM public.application_onboarding_approved_targets m
              JOIN public.application_onboarding_cohort_targets x
                ON (x.operation_id,x.installation_id,x.binding_id)=(o.id,m.installation_id,m.binding_id)
              JOIN public.mobile_installations i ON (i.application_id,i.id)=(a.id,m.installation_id)
              JOIN public.installation_bindings b ON (b.application_id,b.id,b.installation_id)=(a.id,m.binding_id,i.id)
              JOIN public.users u ON (u.application_id,u.id)=(a.id,i.owner_user_id)
              JOIN public.current_binding_routes route ON (route.application_id,route.binding_id)=(a.id,b.id)
              WHERE (m.application_id,m.approval_id)=(a.id,p.id) AND m.revoked_at IS NULL
                AND b.retired_at IS NULL AND u.status='ACTIVE'
                AND s.enrollment_platforms @> jsonb_build_array(jsonb_build_object('platform',b.platform))
                AND (b.owner_user_id,b.owner_epoch)=(i.owner_user_id,i.owner_epoch)
                AND x.outcome='PASSED' AND x.checked_at>t.at-interval '15 minutes' AND x.checked_at<=o.completed_at
                AND route.destination='FCM_ACTIVE' AND route.cohort_id=o.id
                AND (route.origin_epoch,route.source_epoch)=(r.origin_epoch,r.source_epoch))
          -- A route outside the reviewed and checked roster would also be eligible for PILOT.
          AND cardinality(o.installation_ids)=(
              SELECT count(*) FROM public.current_binding_routes route
              WHERE route.application_id=a.id AND route.cohort_id=o.id AND route.destination='FCM_ACTIVE'
                AND (route.origin_epoch,route.source_epoch)=(r.origin_epoch,r.source_epoch))
    );
$$;
REVOKE ALL ON FUNCTION onboarding_pilot_ready(bigint,uuid) FROM PUBLIC;

-- Keep the M39 authority and revision checks. Its apply function already locks
-- the runtime/app/policy; the app lock also orders M37 routes and enrollment.
CREATE FUNCTION require_onboarding_pilot_ready() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    -- The M39 guard runs first and normalizes one-way holds. Legacy apps with
    -- no activated onboarding configuration keep their manual dispatch path.
    IF NEW.dispatch_mode='PILOT' AND NOT NEW.hold_new AND EXISTS (
        SELECT FROM public.application_onboarding s WHERE s.application_id=OLD.application_id
          AND (s.runtime_generation>0 OR s.current_configuration_id IS NOT NULL OR s.cohort_id IS NOT NULL)
    ) AND NOT public.onboarding_pilot_ready(OLD.application_id,NEW.dispatch_cohort)
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='onboarding pilot prerequisites unavailable'; END IF;
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION require_onboarding_pilot_ready() FROM PUBLIC;
CREATE TRIGGER require_onboarding_pilot_ready BEFORE UPDATE OF hold_new,coverage_approved,dispatch_revision,dispatch_mode,
    dispatch_cohort,dispatch_origin_epoch,dispatch_source_epoch ON application_dispatch_policy
    FOR EACH ROW EXECUTE FUNCTION require_onboarding_pilot_ready();
