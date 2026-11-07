-- Removing an identity trigger would turn an existing lock-only column grant
-- into authority to rewrite keys. Hold the affected tables through all DDL.
DO $$
BEGIN
    LOCK TABLE applications,application_dispatch_policy,staff_users,users,notification_send_dead_letters IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (
        SELECT FROM (VALUES
            ('applications'::regclass,'id'),('application_dispatch_policy'::regclass,'application_id'),
            ('staff_users'::regclass,'id'),('users'::regclass,'id'),
            ('notification_send_dead_letters'::regclass,'log_source_id')
        ) AS locked(relation_id,column_name)
        JOIN pg_catalog.pg_class c ON c.oid=locked.relation_id
        JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid AND a.attname=locked.column_name
        CROSS JOIN LATERAL pg_catalog.aclexplode(a.attacl) priv
        WHERE priv.privilege_type='UPDATE' AND priv.grantee<>c.relowner
    ) THEN
        RAISE EXCEPTION USING ERRCODE='23514', MESSAGE='remove lock-only column grants before dropping daemon identity guards';
    END IF;
    DROP FUNCTION clear_finished_replay_claim(bigint,uuid,uuid,integer,bigint,uuid);
    DROP FUNCTION abandon_expired_replay_attempt(bigint,uuid,uuid,integer,bigint,uuid);
    DROP FUNCTION hold_delivery_dispatch(bigint);
    DROP TRIGGER guard_daemon_lock_identity ON notification_send_dead_letters;
    DROP TRIGGER guard_daemon_lock_identity ON users;
    DROP TRIGGER guard_daemon_lock_identity ON staff_users;
    DROP TRIGGER guard_daemon_lock_identity ON application_dispatch_policy;
    DROP TRIGGER guard_daemon_lock_identity ON applications;
    DROP FUNCTION guard_daemon_lock_identity();
END;
$$;
