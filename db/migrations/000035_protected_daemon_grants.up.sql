-- Read locks require UPDATE on one column. These business identities can be
-- locked or assigned their existing value, but cannot be rewritten.
CREATE FUNCTION guard_daemon_lock_identity() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    RAISE EXCEPTION USING ERRCODE='23514', MESSAGE='protected row identity is immutable';
END;
$$;
CREATE TRIGGER guard_daemon_lock_identity BEFORE UPDATE OF id ON applications
    FOR EACH ROW WHEN (OLD.id IS DISTINCT FROM NEW.id) EXECUTE FUNCTION guard_daemon_lock_identity();
CREATE TRIGGER guard_daemon_lock_identity BEFORE UPDATE OF application_id ON application_dispatch_policy
    FOR EACH ROW WHEN (OLD.application_id IS DISTINCT FROM NEW.application_id) EXECUTE FUNCTION guard_daemon_lock_identity();
CREATE TRIGGER guard_daemon_lock_identity BEFORE UPDATE OF id ON staff_users
    FOR EACH ROW WHEN (OLD.id IS DISTINCT FROM NEW.id) EXECUTE FUNCTION guard_daemon_lock_identity();
CREATE TRIGGER guard_daemon_lock_identity BEFORE UPDATE OF id ON users
    FOR EACH ROW WHEN (OLD.id IS DISTINCT FROM NEW.id) EXECUTE FUNCTION guard_daemon_lock_identity();
CREATE TRIGGER guard_daemon_lock_identity BEFORE UPDATE OF log_source_id ON notification_send_dead_letters
    FOR EACH ROW WHEN (OLD.log_source_id IS DISTINCT FROM NEW.log_source_id) EXECUTE FUNCTION guard_daemon_lock_identity();

CREATE FUNCTION hold_delivery_dispatch(target_application bigint) RETURNS void
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
    UPDATE public.application_dispatch_policy SET hold_new=true WHERE application_id=target_application
$$;
REVOKE ALL ON FUNCTION hold_delivery_dispatch(bigint) FROM PUBLIC;

CREATE FUNCTION abandon_expired_replay_attempt(p_app bigint,p_request uuid,p_source uuid,p_partition integer,p_offset bigint,p_claim uuid) RETURNS bigint
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
    WITH changed AS (
        UPDATE public.notification_replay_attempts SET outcome='abandoned_unknown',finished_at=clock_timestamp()
        WHERE application_id=p_app AND request_id=p_request
            AND log_source_id=p_source AND original_partition=p_partition
            AND original_offset=p_offset AND claim_token=p_claim
            AND outcome='started' AND lease_expires_at<=clock_timestamp()
            AND EXISTS(SELECT 1 FROM public.notification_replay_controls c
                WHERE c.log_source_id=p_source AND c.original_partition=p_partition
                AND c.original_offset=p_offset AND c.active_application_id=p_app
                AND c.active_request_id=p_request AND c.active_claim_token=p_claim)
        RETURNING 1
    ) SELECT count(*) FROM changed
$$;
REVOKE ALL ON FUNCTION abandon_expired_replay_attempt(bigint,uuid,uuid,integer,bigint,uuid) FROM PUBLIC;

CREATE FUNCTION clear_finished_replay_claim(p_app bigint,p_request uuid,p_source uuid,p_partition integer,p_offset bigint,p_claim uuid) RETURNS bigint
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
    WITH changed AS (
        UPDATE public.notification_replay_controls SET active_application_id=NULL,active_request_id=NULL,active_claim_token=NULL
        WHERE log_source_id=p_source AND original_partition=p_partition
            AND original_offset=p_offset AND active_application_id=p_app
            AND active_request_id=p_request AND active_claim_token=p_claim
            AND EXISTS(SELECT 1 FROM public.notification_replay_attempts a
                WHERE a.application_id=p_app AND a.request_id=p_request
                AND a.log_source_id=p_source AND a.original_partition=p_partition
                AND a.original_offset=p_offset AND a.claim_token=p_claim
                AND a.outcome IN ('held','rejected','expired','reconciliation_required','publish_unconfirmed','abandoned_unknown'))
        RETURNING 1
    ) SELECT count(*) FROM changed
$$;
REVOKE ALL ON FUNCTION clear_finished_replay_claim(bigint,uuid,uuid,integer,bigint,uuid) FROM PUBLIC;
