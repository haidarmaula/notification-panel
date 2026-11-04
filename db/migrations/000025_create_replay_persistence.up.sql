-- Replay is a restricted operational workflow, not a new-send API.
-- The operational grant UUID is provenance; service-side verification and
-- revocation checks are mandatory before creating a request or claim.
CREATE TABLE notification_replay_requests (
    application_id BIGINT NOT NULL REFERENCES applications(id) ON DELETE RESTRICT,
    id UUID NOT NULL CHECK (id <> '00000000-0000-0000-0000-000000000000'),
    staff_user_id BIGINT NOT NULL REFERENCES staff_users(id) ON DELETE RESTRICT,
    operational_grant_id UUID NOT NULL CHECK (operational_grant_id <> '00000000-0000-0000-0000-000000000000'),
    selection_hash BYTEA NOT NULL CHECK (octet_length(selection_hash)=32),
    change_reference TEXT NOT NULL CHECK (octet_length(change_reference) BETWEEN 1 AND 255
        AND change_reference=btrim(change_reference) AND change_reference !~ '[[:cntrl:]]'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (application_id,id)
);

CREATE TABLE notification_replay_attempts (
    application_id BIGINT NOT NULL,
    request_id UUID NOT NULL,
    log_source_id UUID NOT NULL,
    original_partition INTEGER NOT NULL,
    original_offset BIGINT NOT NULL,
    notification_id BIGINT,
    origin_epoch UUID,
    source_epoch UUID,
    claim_token UUID CHECK (claim_token <> '00000000-0000-0000-0000-000000000000'),
    lease_expires_at TIMESTAMPTZ,
    outcome TEXT NOT NULL CHECK (outcome IN ('started','held','rejected','expired','reconciliation_required',
        'already_prepared','terminal','published','publish_unconfirmed','abandoned_unknown')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    finished_at TIMESTAMPTZ,
    PRIMARY KEY (application_id,request_id,log_source_id,original_partition,original_offset),
    FOREIGN KEY (application_id,request_id) REFERENCES notification_replay_requests(application_id,id) ON DELETE RESTRICT,
    FOREIGN KEY (log_source_id,original_partition,original_offset)
        REFERENCES notification_send_dead_letters(log_source_id,original_partition,original_offset) ON DELETE RESTRICT,
    FOREIGN KEY (application_id,notification_id) REFERENCES notifications(application_id,id) ON DELETE RESTRICT,
    CHECK ((notification_id IS NULL AND origin_epoch IS NULL AND source_epoch IS NULL)
        OR (notification_id IS NOT NULL AND origin_epoch IS NOT NULL AND source_epoch IS NOT NULL)),
    CHECK ((claim_token IS NULL)=(lease_expires_at IS NULL)),
    CHECK (lease_expires_at IS NULL OR lease_expires_at>created_at),
    CHECK ((outcome='started')=(finished_at IS NULL)),
    CHECK (finished_at IS NULL OR finished_at>=created_at),
    CHECK (outcome NOT IN ('started','published','publish_unconfirmed','abandoned_unknown')
        OR (claim_token IS NOT NULL AND notification_id IS NOT NULL)),
    CHECK (outcome NOT IN ('already_prepared','terminal') OR notification_id IS NOT NULL),
    UNIQUE (application_id,request_id,log_source_id,original_partition,original_offset,claim_token),
    UNIQUE (application_id,request_id,log_source_id,original_partition,original_offset,outcome)
);

-- Exactly one physical-position control row serializes all apps and requests.
-- Lease time remains on its immutable attempt, never in a time-dependent index.
CREATE TABLE notification_replay_controls (
    log_source_id UUID NOT NULL,
    original_partition INTEGER NOT NULL,
    original_offset BIGINT NOT NULL,
    active_application_id BIGINT,
    active_request_id UUID,
    active_claim_token UUID,
    resolved_application_id BIGINT,
    resolved_request_id UUID,
    resolved_outcome TEXT CHECK (resolved_outcome IN ('published','already_prepared','terminal')),
    PRIMARY KEY (log_source_id,original_partition,original_offset),
    FOREIGN KEY (log_source_id,original_partition,original_offset)
        REFERENCES notification_send_dead_letters(log_source_id,original_partition,original_offset) ON DELETE RESTRICT,
    CHECK ((active_application_id IS NULL AND active_request_id IS NULL AND active_claim_token IS NULL)
        OR (active_application_id IS NOT NULL AND active_request_id IS NOT NULL AND active_claim_token IS NOT NULL)),
    CHECK ((resolved_application_id IS NULL AND resolved_request_id IS NULL AND resolved_outcome IS NULL)
        OR (resolved_application_id IS NOT NULL AND resolved_request_id IS NOT NULL AND resolved_outcome IS NOT NULL)),
    CHECK (active_claim_token IS NULL OR resolved_outcome IS NULL),
    FOREIGN KEY (active_application_id,active_request_id,log_source_id,original_partition,original_offset,active_claim_token)
        REFERENCES notification_replay_attempts(application_id,request_id,log_source_id,original_partition,original_offset,claim_token) ON DELETE RESTRICT,
    FOREIGN KEY (resolved_application_id,resolved_request_id,log_source_id,original_partition,original_offset,resolved_outcome)
        REFERENCES notification_replay_attempts(application_id,request_id,log_source_id,original_partition,original_offset,outcome) ON DELETE RESTRICT
);
CREATE INDEX idx_replay_attempts_expired ON notification_replay_attempts(lease_expires_at,log_source_id,original_partition,original_offset)
    WHERE outcome='started';
CREATE INDEX idx_replay_requests_staff ON notification_replay_requests(staff_user_id);
CREATE INDEX idx_replay_attempts_campaign ON notification_replay_attempts(application_id,notification_id);

CREATE FUNCTION guard_replay_request_update() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='replay request identity is immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER guard_replay_request_update BEFORE UPDATE ON notification_replay_requests
    FOR EACH ROW EXECUTE FUNCTION guard_replay_request_update();

CREATE FUNCTION guard_replay_attempt_insert() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF NEW.outcome IN ('published','publish_unconfirmed','abandoned_unknown')
       OR (NEW.outcome<>'started' AND NEW.claim_token IS NOT NULL)
    THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='replay result requires a claimed attempt';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER guard_replay_attempt_insert BEFORE INSERT ON notification_replay_attempts
    FOR EACH ROW EXECUTE FUNCTION guard_replay_attempt_insert();

CREATE FUNCTION guard_replay_attempt_update() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF ROW(NEW.application_id,NEW.request_id,NEW.log_source_id,NEW.original_partition,NEW.original_offset,
        NEW.notification_id,NEW.origin_epoch,NEW.source_epoch,NEW.claim_token,NEW.lease_expires_at,NEW.created_at)
       IS DISTINCT FROM ROW(OLD.application_id,OLD.request_id,OLD.log_source_id,OLD.original_partition,OLD.original_offset,
        OLD.notification_id,OLD.origin_epoch,OLD.source_epoch,OLD.claim_token,OLD.lease_expires_at,OLD.created_at)
       OR (OLD.outcome<>'started' AND NEW IS DISTINCT FROM OLD)
    THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='replay attempt identity or result is immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER guard_replay_attempt_update BEFORE UPDATE ON notification_replay_attempts
    FOR EACH ROW EXECUTE FUNCTION guard_replay_attempt_update();

CREATE FUNCTION guard_replay_control_update() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF ROW(NEW.log_source_id,NEW.original_partition,NEW.original_offset)
       IS DISTINCT FROM ROW(OLD.log_source_id,OLD.original_partition,OLD.original_offset)
       OR (OLD.resolved_outcome IS NOT NULL AND NEW IS DISTINCT FROM OLD)
    THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='replay source identity or resolution is immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER guard_replay_control_update BEFORE UPDATE ON notification_replay_controls
    FOR EACH ROW EXECUTE FUNCTION guard_replay_control_update();
