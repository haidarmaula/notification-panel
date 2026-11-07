ALTER TABLE staff_users ADD COLUMN auth_epoch bigint NOT NULL DEFAULT 1 CHECK(auth_epoch>0);
CREATE FUNCTION advance_staff_auth_epoch() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF ROW(NEW.role_id,NEW.is_active,NEW.password_hash) IS DISTINCT FROM ROW(OLD.role_id,OLD.is_active,OLD.password_hash) THEN
        NEW.auth_epoch := OLD.auth_epoch+1;
    ELSIF NEW.auth_epoch < OLD.auth_epoch THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='staff authentication generation cannot decrease';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER staff_auth_epoch_change BEFORE UPDATE ON staff_users FOR EACH ROW EXECUTE FUNCTION advance_staff_auth_epoch();

-- Recovery operators rotate the singleton during a sealed restore. A change
-- invalidates all prior families without rewriting staff or session history.
CREATE TABLE staff_session_control (
    singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
    session_epoch uuid NOT NULL DEFAULT gen_random_uuid()
);
INSERT INTO staff_session_control(singleton) VALUES(true);

CREATE TABLE staff_sessions (
    session_epoch uuid NOT NULL,
    id uuid PRIMARY KEY,
    staff_user_id bigint REFERENCES staff_users(id) ON DELETE SET NULL,
    auth_epoch bigint NOT NULL CHECK(auth_epoch>0),
    refresh_digest bytea NOT NULL CHECK(octet_length(refresh_digest)=32),
    generation bigint NOT NULL DEFAULT 1 CHECK(generation>0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL CHECK(isfinite(expires_at) AND expires_at>created_at),
    rotated_at timestamptz,
    revoked_at timestamptz,
    revocation_reason text CHECK(revocation_reason IN ('logout','refresh_reuse','recovery')),
    CHECK((revoked_at IS NULL)=(revocation_reason IS NULL))
);
CREATE INDEX idx_staff_sessions_user ON staff_sessions(staff_user_id);
