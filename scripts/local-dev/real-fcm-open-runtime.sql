-- Local real-FCM profile only. This opens worker runtime after a guarded fresh-state preflight.
DO $$
DECLARE
    application_key uuid := '10000000-0000-4000-8000-000000000003';
    current_state record;
BEGIN
    IF current_database() <> 'notification_integration'
       OR NOT EXISTS (SELECT FROM pg_roles WHERE rolname=session_user AND rolsuper)
       OR (SELECT count(*) FROM applications WHERE public_id=application_key AND status='ACTIVE') <> 1
       OR NOT EXISTS (
           SELECT FROM applications a JOIN application_dispatch_policy p ON p.application_id=a.id
           WHERE a.public_id=application_key AND p.dispatch_mode='HELD' AND p.hold_new AND NOT p.coverage_approved
       )
       OR EXISTS (
           SELECT FROM current_binding_routes r
           JOIN installation_bindings b ON (b.application_id,b.id)=(r.application_id,r.binding_id) AND b.retired_at IS NULL
           JOIN applications a ON a.id=r.application_id
           WHERE a.public_id<>application_key
       )
       OR (SELECT count(*) FROM current_binding_routes r
           JOIN installation_bindings b ON (b.application_id,b.id)=(r.application_id,r.binding_id) AND b.retired_at IS NULL
           JOIN applications a ON a.id=r.application_id
           WHERE a.public_id=application_key AND r.destination='FCM_ACTIVE') > 1
    THEN
        RAISE EXCEPTION 'real FCM local runtime preflight failed';
    END IF;

    SELECT sealed,migration_hold INTO STRICT current_state FROM notification_runtime WHERE singleton FOR UPDATE;
    IF current_state.sealed AND current_state.migration_hold THEN
        IF EXISTS (SELECT FROM notifications)
           OR EXISTS (SELECT FROM notification_delivery_intents)
           OR EXISTS (SELECT FROM notification_send_outbox)
           OR EXISTS (SELECT FROM notification_update_outbox)
        THEN
            RAISE EXCEPTION 'real FCM runtime contains prior delivery state';
        END IF;
        UPDATE notification_runtime SET sealed=false,migration_hold=false WHERE singleton;
    ELSIF current_state.sealed OR current_state.migration_hold THEN
        RAISE EXCEPTION 'real FCM runtime state is inconsistent';
    END IF;
END;
$$;
