CREATE FUNCTION guard_server_lock_only_update() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD AND (
        current_user='hello_server' OR EXISTS (
            SELECT FROM pg_auth_members m JOIN pg_roles g ON g.oid=m.roleid JOIN pg_roles u ON u.oid=m.member
            WHERE g.rolname='hello_server' AND u.rolname=current_user
        )
    ) THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='server lock-only identity is immutable';
    END IF;
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION guard_server_lock_only_update() FROM PUBLIC;

CREATE TRIGGER guard_server_lock_only_update BEFORE UPDATE ON mobile_identities
    FOR EACH ROW EXECUTE FUNCTION guard_server_lock_only_update();
CREATE TRIGGER guard_server_lock_only_update BEFORE UPDATE ON notification_send_keys
    FOR EACH ROW EXECUTE FUNCTION guard_server_lock_only_update();
