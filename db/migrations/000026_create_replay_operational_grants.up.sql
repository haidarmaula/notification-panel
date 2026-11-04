-- Provision through a restricted operational issuer/channel, never staff CRUD.
CREATE TABLE notification_replay_grants (
    id UUID PRIMARY KEY CHECK (id<>'00000000-0000-0000-0000-000000000000'),
    application_id BIGINT NOT NULL REFERENCES applications(id) ON DELETE RESTRICT,
    staff_user_id BIGINT NOT NULL REFERENCES staff_users(id) ON DELETE RESTRICT,
    request_id UUID NOT NULL CHECK (request_id<>'00000000-0000-0000-0000-000000000000'),
    issuer TEXT NOT NULL CHECK (octet_length(issuer) BETWEEN 1 AND 255 AND issuer=btrim(issuer) AND issuer !~ '[[:cntrl:]]'),
    deployment TEXT NOT NULL CHECK (octet_length(deployment) BETWEEN 1 AND 255 AND deployment=btrim(deployment) AND deployment !~ '[[:cntrl:]]'),
    selection_hash BYTEA NOT NULL CHECK (octet_length(selection_hash)=32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    CHECK (expires_at>created_at),
    CHECK (revoked_at IS NULL OR revoked_at>=created_at)
);
CREATE INDEX idx_replay_grants_application ON notification_replay_grants(application_id);
CREATE INDEX idx_replay_grants_staff ON notification_replay_grants(staff_user_id);

CREATE FUNCTION guard_replay_grant_update() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF ROW(NEW.id,NEW.application_id,NEW.staff_user_id,NEW.request_id,NEW.issuer,NEW.deployment,NEW.selection_hash,NEW.created_at,NEW.expires_at)
       IS DISTINCT FROM ROW(OLD.id,OLD.application_id,OLD.staff_user_id,OLD.request_id,OLD.issuer,OLD.deployment,OLD.selection_hash,OLD.created_at,OLD.expires_at)
       OR (OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at)
    THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='replay grant identity and revocation are immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER guard_replay_grant_update BEFORE UPDATE ON notification_replay_grants
    FOR EACH ROW EXECUTE FUNCTION guard_replay_grant_update();
