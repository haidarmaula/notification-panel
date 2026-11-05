ALTER TABLE notification_delivery_intents
    ADD COLUMN provider_message_id TEXT CHECK (octet_length(provider_message_id) BETWEEN 1 AND 1024),
    ADD COLUMN provider_accepted_at TIMESTAMPTZ,
    ADD COLUMN observed_delivered_at TIMESTAMPTZ,
    ADD COLUMN observed_opened_at TIMESTAMPTZ,
    ADD COLUMN failed_at TIMESTAMPTZ,
    ADD COLUMN terminal_reason TEXT CHECK (octet_length(terminal_reason) BETWEEN 1 AND 64 AND terminal_reason !~ '[^a-z0-9_]'),
    ADD COLUMN last_result_disposition TEXT CHECK (last_result_disposition IN ('accepted','rejected','unconfirmed','not_submitted')),
    ADD COLUMN last_result_reason TEXT CHECK (octet_length(last_result_reason) BETWEEN 1 AND 64 AND last_result_reason !~ '[^a-z0-9_]'),
    ADD COLUMN last_result_fingerprint BYTEA CHECK (octet_length(last_result_fingerprint)=32),
    ADD CONSTRAINT delivery_provider_evidence_pair CHECK ((provider_message_id IS NULL)=(provider_accepted_at IS NULL)),
    ADD CONSTRAINT delivery_result_evidence_pair CHECK (
        (last_result_fingerprint IS NULL AND last_result_disposition IS NULL AND last_result_reason IS NULL)
        OR (last_result_fingerprint IS NOT NULL AND last_result_disposition IS NOT NULL AND last_result_reason IS NOT NULL));

ALTER TABLE notification_delivery_intents ADD CONSTRAINT uq_delivery_update_identity UNIQUE(application_id,notification_id,id);

CREATE TABLE notification_update_outbox (
    event_id UUID PRIMARY KEY,
    application_id BIGINT NOT NULL,
    notification_id BIGINT NOT NULL,
    delivery_id UUID NOT NULL,
    delivery_version BIGINT NOT NULL CHECK (delivery_version>0),
    origin_epoch UUID NOT NULL,
    source_epoch UUID NOT NULL,
    payload BYTEA NOT NULL CHECK (octet_length(payload) BETWEEN 1 AND 65536),
    occurred_at TIMESTAMPTZ NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts>=0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(source_epoch,delivery_id,delivery_version),
    FOREIGN KEY(application_id,notification_id,delivery_id) REFERENCES notification_delivery_intents(application_id,notification_id,id) ON DELETE RESTRICT,
    FOREIGN KEY(application_id,notification_id) REFERENCES notifications(application_id,id) ON DELETE RESTRICT
);
CREATE INDEX idx_update_outbox_due ON notification_update_outbox(next_attempt_at,event_id);
CREATE INDEX idx_update_outbox_predecessor ON notification_update_outbox(delivery_id,delivery_version);

CREATE FUNCTION guard_delivery_update_envelope() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF ROW(NEW.event_id,NEW.application_id,NEW.notification_id,NEW.delivery_id,NEW.delivery_version,
           NEW.origin_epoch,NEW.source_epoch,NEW.payload,NEW.occurred_at)
       IS DISTINCT FROM
       ROW(OLD.event_id,OLD.application_id,OLD.notification_id,OLD.delivery_id,OLD.delivery_version,
           OLD.origin_epoch,OLD.source_epoch,OLD.payload,OLD.occurred_at)
    THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='delivery update envelope is immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER guard_delivery_update_envelope BEFORE UPDATE ON notification_update_outbox
    FOR EACH ROW EXECUTE FUNCTION guard_delivery_update_envelope();
