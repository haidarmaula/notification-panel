-- Record provenance only for new, first attempts. A receipt carries a delivery
-- identity, not an attempt identity, so retries cannot prove which send arrived.
CREATE TABLE hello_delivery_internal.onboarding_pilot_attempts (
    application_id bigint NOT NULL,
    delivery_id uuid PRIMARY KEY,
    attempt_id uuid NOT NULL,
    dispatch_grant_id uuid NOT NULL REFERENCES public.application_dispatch_grants(id),
    configuration_id uuid NOT NULL,
    runtime_generation bigint NOT NULL,
    worker_role oid NOT NULL,
    accepted_at timestamptz,
    FOREIGN KEY (application_id,delivery_id) REFERENCES public.notification_delivery_intents(application_id,id)
);
REVOKE ALL ON hello_delivery_internal.onboarding_pilot_attempts FROM PUBLIC;

CREATE FUNCTION hello_delivery_internal.record_onboarding_pilot_attempt() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    IF OLD.attempt_count=0 AND NEW.attempt_count=1 AND NEW.attempt_id IS NOT NULL THEN
        INSERT INTO hello_delivery_internal.onboarding_pilot_attempts
            (application_id,delivery_id,attempt_id,dispatch_grant_id,configuration_id,runtime_generation,worker_role)
        SELECT s.application_id,NEW.id,NEW.attempt_id,g.id,w.configuration_id,w.generation,w.worker_role
        FROM public.application_onboarding s
        JOIN public.application_dispatch_policy d USING (application_id)
        JOIN public.application_worker_preflight_scopes w USING (application_id)
        JOIN public.application_runtime_reservations v
          ON (v.application_id,v.generation,v.configuration_id)=(w.application_id,w.generation,w.configuration_id)
        JOIN public.applications a ON a.id=s.application_id
        JOIN public.application_dispatch_grants g ON g.project_id=a.public_id
        JOIN public.notification_preparations p ON (p.application_id,p.notification_id)=(s.application_id,NEW.notification_id)
        JOIN public.notifications n ON (n.application_id,n.id,n.origin_epoch,n.source_epoch)=(p.application_id,p.notification_id,p.origin_epoch,p.source_epoch)
        JOIN public.current_binding_routes b ON (b.application_id,b.binding_id)=(s.application_id,NEW.binding_id)
        CROSS JOIN public.notification_runtime r
        WHERE s.application_id=NEW.application_id AND r.singleton AND NOT r.sealed AND NOT r.migration_hold
          AND w.worker_role=session_user::regrole::oid AND v.released_at IS NULL AND w.environment=s.environment
          AND (w.configuration_id,w.generation)=(s.current_configuration_id,s.runtime_generation)
          AND (v.origin_epoch,v.source_epoch)=(r.origin_epoch,r.source_epoch)
          AND d.dispatch_mode='PILOT' AND NOT d.hold_new AND g.destination='PILOT'
          AND g.expected_revision=d.dispatch_revision-1 AND g.application_revision=a.revision
          AND g.cohort_id=d.dispatch_cohort AND g.cohort_id=s.cohort_id
          AND (g.origin_epoch,g.source_epoch)=(r.origin_epoch,r.source_epoch)
          AND (d.dispatch_origin_epoch,d.dispatch_source_epoch)=(r.origin_epoch,r.source_epoch)
          AND (p.origin_epoch,p.source_epoch)=(r.origin_epoch,r.source_epoch)
          AND p.prepared_pilot_cohort=g.cohort_id AND g.applied_at<=NEW.created_at AND p.completed_at<=NEW.last_attempt_at
          AND b.destination='FCM_ACTIVE' AND b.cohort_id=g.cohort_id AND b.revision=NEW.audience_route_revision
          AND (b.origin_epoch,b.source_epoch)=(r.origin_epoch,r.source_epoch);
    ELSIF NEW.attempt_count=1 AND NEW.attempt_id=OLD.attempt_id AND OLD.attempt_resolved_at IS NULL
        AND OLD.provider_accepted_at IS NULL AND NEW.last_result_disposition='accepted'
        AND NEW.provider_accepted_at>=NEW.last_attempt_at AND NEW.attempt_resolved_at IS NOT NULL THEN
        UPDATE hello_delivery_internal.onboarding_pilot_attempts SET accepted_at=NEW.provider_accepted_at
        WHERE application_id=NEW.application_id AND delivery_id=NEW.id AND attempt_id=NEW.attempt_id
          AND worker_role=session_user::regrole::oid AND accepted_at IS NULL;
    END IF;
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION hello_delivery_internal.record_onboarding_pilot_attempt() FROM PUBLIC;
CREATE TRIGGER record_onboarding_pilot_attempt
    AFTER UPDATE OF attempt_id,provider_accepted_at ON public.notification_delivery_intents
    FOR EACH ROW EXECUTE FUNCTION hello_delivery_internal.record_onboarding_pilot_attempt();

CREATE FUNCTION require_onboarding_full_ready() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    -- M39 runs first. Preserve its one-way holds and the legacy manual path.
    IF NEW.dispatch_mode<>'FULL' OR NEW.hold_new OR NOT EXISTS (
        SELECT FROM public.application_onboarding s WHERE s.application_id=OLD.application_id
          AND (s.runtime_generation>0 OR s.current_configuration_id IS NOT NULL OR s.cohort_id IS NOT NULL)
    ) THEN RETURN NEW; END IF;

    IF OLD.dispatch_mode<>'PILOT' OR OLD.hold_new OR NOT EXISTS (
        SELECT FROM public.application_onboarding s
        JOIN public.applications a ON a.id=s.application_id AND a.status='ACTIVE'
        JOIN public.application_onboarding_operations o ON (o.application_id,o.id)=(s.application_id,s.cohort_id)
        JOIN public.application_onboarding_approvals approval ON (approval.application_id,approval.id)=(s.application_id,o.approval_id)
        JOIN public.application_provider_configurations c ON (c.application_id,c.id)=(s.application_id,s.current_configuration_id)
        JOIN public.application_dispatch_grants g ON g.project_id=a.public_id
        JOIN public.application_runtime_reservations v ON (v.application_id,v.generation,v.configuration_id)=(s.application_id,s.runtime_generation,c.id)
        JOIN public.application_runtime_capacity_profile profile ON profile.environment=s.environment
        CROSS JOIN public.notification_runtime r
        WHERE s.application_id=OLD.application_id AND s.cohort_id=OLD.dispatch_cohort
          AND r.singleton AND NOT r.sealed AND NOT r.migration_hold
          AND (OLD.dispatch_origin_epoch,OLD.dispatch_source_epoch)=(r.origin_epoch,r.source_epoch)
          AND (NEW.dispatch_origin_epoch,NEW.dispatch_source_epoch)=(r.origin_epoch,r.source_epoch)
          AND c.environment=s.environment AND c.state='VERIFIED' AND c.file_reference=c.id
          AND v.released_at IS NULL AND (v.origin_epoch,v.source_epoch)=(r.origin_epoch,r.source_epoch)
          AND o.kind='TEST_COHORT' AND o.state='SUCCEEDED' AND o.completed_by IS NOT NULL AND o.configuration_id=c.id
          AND o.expected_dispatch_revision=OLD.dispatch_revision-1 AND o.expected_application_revision=a.revision
          AND (o.origin_epoch,o.source_epoch)=(r.origin_epoch,r.source_epoch)
          AND approval.revoked_at IS NULL AND approval.profile_revision=profile.revision
          AND g.destination='PILOT' AND g.applied_at>=o.completed_at AND g.expected_revision=OLD.dispatch_revision-1
          AND g.cohort_id=o.id AND g.application_revision=a.revision
          AND (g.origin_epoch,g.source_epoch)=(r.origin_epoch,r.source_epoch)
          AND EXISTS (SELECT FROM public.application_dispatch_grants release
              WHERE release.project_id=a.public_id AND release.destination='FULL' AND release.applied_at IS NOT NULL
                AND release.expected_revision=OLD.dispatch_revision AND release.cohort_id=o.id)
          AND s.enrollment_profile_id IS NOT NULL AND jsonb_array_length(s.enrollment_platforms)>0
          AND NOT EXISTS (
              SELECT FROM jsonb_array_elements(s.enrollment_platforms) AS configured(platform)
              WHERE NOT EXISTS (
                  SELECT FROM public.application_onboarding_cohort_targets x
                  JOIN public.application_onboarding_approved_targets m
                    ON (m.application_id,m.approval_id,m.installation_id,m.binding_id)=(s.application_id,approval.id,x.installation_id,x.binding_id)
                  JOIN public.installation_bindings b ON (b.application_id,b.id,b.installation_id)=(s.application_id,x.binding_id,x.installation_id)
                  JOIN public.mobile_installations i ON (i.application_id,i.id,i.owner_user_id,i.owner_epoch)=(b.application_id,b.installation_id,b.owner_user_id,b.owner_epoch)
                  JOIN public.users u ON (u.application_id,u.id)=(b.application_id,b.owner_user_id)
                  JOIN public.current_binding_routes route ON (route.application_id,route.binding_id)=(b.application_id,b.id)
                  JOIN public.notification_delivery_intents d ON (d.application_id,d.binding_id,d.installation_id,d.owner_user_id,d.owner_epoch)
                    =(b.application_id,b.id,b.installation_id,b.owner_user_id,b.owner_epoch)
                  JOIN hello_delivery_internal.onboarding_pilot_attempts proof ON (proof.application_id,proof.delivery_id,proof.attempt_id)=(d.application_id,d.id,d.attempt_id)
                  JOIN public.notification_receipt_metadata metadata ON (metadata.application_id,metadata.delivery_id)=(d.application_id,d.id)
                  JOIN public.notification_observations receipt ON (receipt.application_id,receipt.delivery_id)=(d.application_id,d.id)
                  WHERE x.operation_id=o.id AND x.outcome='PASSED' AND m.revoked_at IS NULL
                    AND b.platform=configured.platform->>'platform' AND b.retired_at IS NULL AND u.status='ACTIVE'
                    AND route.destination='FCM_ACTIVE' AND route.cohort_id=o.id AND route.revision=d.audience_route_revision
                    AND (route.origin_epoch,route.source_epoch)=(r.origin_epoch,r.source_epoch)
                    AND (proof.dispatch_grant_id,proof.configuration_id,proof.runtime_generation)=(g.id,c.id,s.runtime_generation)
                    AND d.attempt_count=1 AND d.last_result_disposition='accepted' AND d.provider_message_id IS NOT NULL
                    AND d.provider_accepted_at=proof.accepted_at AND d.failed_at IS NULL AND d.status IN ('DELIVERED','OPENED')
                    -- This fact is written only after verifying the frozen signed capability.
                    -- Receipt ingestion can race ahead of result persistence.
                    AND receipt.event='DELIVERED' AND receipt.received_at=d.observed_delivered_at
                    AND receipt.received_at>=d.last_attempt_at AND receipt.received_at<to_timestamp(metadata.expires_unix)
              )
          )
    ) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='onboarding full prerequisites unavailable'; END IF;
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION require_onboarding_full_ready() FROM PUBLIC;
CREATE TRIGGER require_onboarding_full_ready BEFORE UPDATE OF hold_new,coverage_approved,dispatch_revision,dispatch_mode,
    dispatch_cohort,dispatch_origin_epoch,dispatch_source_epoch ON public.application_dispatch_policy
    FOR EACH ROW EXECUTE FUNCTION require_onboarding_full_ready();
