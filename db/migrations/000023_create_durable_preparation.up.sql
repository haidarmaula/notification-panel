ALTER TABLE notifications
    ADD COLUMN preparation_hold_reason TEXT,
    ADD COLUMN preparation_next_attempt_at TIMESTAMPTZ;
CREATE INDEX idx_scoped_preparation_recovery ON notifications(preparation_next_attempt_at,id)
    WHERE status='QUEUED' AND deadline_at IS NOT NULL;

ALTER TABLE installation_bindings ADD CONSTRAINT uq_binding_intent_owner
    UNIQUE (application_id,id,installation_id,owner_user_id,owner_epoch);

CREATE TABLE notification_preparations (
    notification_id BIGINT PRIMARY KEY,
    application_id BIGINT NOT NULL,
    origin_epoch UUID NOT NULL,
    source_epoch UUID NOT NULL,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    resolved_user_count BIGINT NOT NULL CHECK (resolved_user_count>=0),
    eligible_user_count BIGINT NOT NULL CHECK (eligible_user_count>=0 AND eligible_user_count<=resolved_user_count),
    eligible_device_count BIGINT NOT NULL CHECK (eligible_device_count>=eligible_user_count),
    FOREIGN KEY (application_id,notification_id) REFERENCES notifications(application_id,id) ON DELETE RESTRICT
);

CREATE TABLE notification_delivery_intents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id BIGINT NOT NULL,
    notification_id BIGINT NOT NULL,
    installation_id BIGINT NOT NULL,
    binding_id BIGINT NOT NULL,
    owner_user_id BIGINT NOT NULL,
    owner_epoch BIGINT NOT NULL CHECK (owner_epoch>0),
    delivery_version BIGINT NOT NULL DEFAULT 0 CHECK (delivery_version>=0),
    status TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','SENT','DELIVERED','OPENED','FAILED')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count>=0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (notification_id,binding_id),
    FOREIGN KEY (application_id,notification_id) REFERENCES notifications(application_id,id) ON DELETE RESTRICT,
    FOREIGN KEY (application_id,binding_id,installation_id,owner_user_id,owner_epoch)
        REFERENCES installation_bindings(application_id,id,installation_id,owner_user_id,owner_epoch) ON DELETE RESTRICT
);
CREATE INDEX idx_delivery_intents_pending ON notification_delivery_intents(notification_id,next_attempt_at,id)
    WHERE status='PENDING';

CREATE FUNCTION guard_preparation_update() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='preparation snapshot is immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER guard_preparation_update BEFORE UPDATE ON notification_preparations
    FOR EACH ROW EXECUTE FUNCTION guard_preparation_update();

CREATE FUNCTION guard_delivery_intent_update() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF ROW(NEW.id,NEW.application_id,NEW.notification_id,NEW.installation_id,NEW.binding_id,
           NEW.owner_user_id,NEW.owner_epoch,NEW.created_at)
       IS DISTINCT FROM
       ROW(OLD.id,OLD.application_id,OLD.notification_id,OLD.installation_id,OLD.binding_id,
           OLD.owner_user_id,OLD.owner_epoch,OLD.created_at)
       OR NEW.delivery_version<OLD.delivery_version OR NEW.attempt_count<OLD.attempt_count
    THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='invalid delivery intent identity or version change';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER guard_delivery_intent_update BEFORE UPDATE ON notification_delivery_intents
    FOR EACH ROW EXECUTE FUNCTION guard_delivery_intent_update();
