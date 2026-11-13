DO $$
DECLARE server_role oid:=to_regrole('hello_server');
BEGIN
    IF server_role IS NOT NULL AND (
        has_column_privilege(server_role,'public.mobile_identities'::regclass,'user_id','UPDATE')
        OR has_column_privilege(server_role,'public.notification_send_keys'::regclass,'key','UPDATE')
    ) THEN
        RAISE EXCEPTION 'revoke server lock-only column grants before removing identity guards';
    END IF;
END;
$$;

DROP TRIGGER guard_server_lock_only_update ON notification_send_keys;
DROP TRIGGER guard_server_lock_only_update ON mobile_identities;
DROP FUNCTION guard_server_lock_only_update();
