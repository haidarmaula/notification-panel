CREATE TABLE notification_log_sources (
    id UUID PRIMARY KEY,
    topic TEXT NOT NULL CHECK (octet_length(topic) BETWEEN 1 AND 249),
    cluster_identity TEXT NOT NULL CHECK (octet_length(cluster_identity) BETWEEN 1 AND 255),
    topic_incarnation TEXT NOT NULL CHECK (octet_length(topic_incarnation) BETWEEN 1 AND 255),
    active BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (cluster_identity,topic,topic_incarnation)
);
CREATE FUNCTION guard_send_log_source_update() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF ROW(NEW.id,NEW.topic,NEW.cluster_identity,NEW.topic_incarnation,NEW.created_at)
       IS DISTINCT FROM ROW(OLD.id,OLD.topic,OLD.cluster_identity,OLD.topic_incarnation,OLD.created_at)
    THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='send log source identity is immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER guard_send_log_source_update BEFORE UPDATE ON notification_log_sources
    FOR EACH ROW EXECUTE FUNCTION guard_send_log_source_update();
CREATE TABLE notification_send_dead_letters (
    log_source_id UUID NOT NULL REFERENCES notification_log_sources(id) ON DELETE RESTRICT,
    original_partition INTEGER NOT NULL CHECK (original_partition>=0),
    original_offset BIGINT NOT NULL CHECK (original_offset>=0),
    original_key BYTEA NOT NULL CHECK (octet_length(original_key)<=1048576),
    original_payload BYTEA NOT NULL CHECK (octet_length(original_payload)<=1048576),
    reason TEXT NOT NULL CHECK (reason IN ('invalid_envelope','acceptance_mismatch','unsupported_targets','preparation_held','preparation_state','admission_exhausted')),
    attempts INTEGER NOT NULL CHECK (attempts BETWEEN 1 AND 3),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (log_source_id,original_partition,original_offset)
);
