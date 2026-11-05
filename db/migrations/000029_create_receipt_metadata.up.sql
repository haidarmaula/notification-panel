ALTER TABLE notification_delivery_intents ADD CONSTRAINT uq_delivery_application_identity UNIQUE(application_id,id);

CREATE TABLE notification_receipt_metadata (
    delivery_id UUID PRIMARY KEY,
    application_id BIGINT NOT NULL,
    version SMALLINT NOT NULL CHECK (version=1),
    key_id TEXT COLLATE "C" NOT NULL CHECK (octet_length(key_id) BETWEEN 1 AND 32 AND key_id !~ '[^A-Za-z0-9_-]'),
    environment TEXT COLLATE "C" NOT NULL CHECK (octet_length(environment) BETWEEN 1 AND 64 AND environment !~ '[^A-Za-z0-9_-]'),
    expires_unix BIGINT NOT NULL CHECK (expires_unix BETWEEN 1 AND 253402300799),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (application_id,delivery_id) REFERENCES notification_delivery_intents(application_id,id) ON DELETE RESTRICT
);
CREATE FUNCTION guard_receipt_metadata() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF TG_OP='DELETE' OR NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='receipt metadata is immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER guard_receipt_metadata BEFORE UPDATE OR DELETE ON notification_receipt_metadata
    FOR EACH ROW EXECUTE FUNCTION guard_receipt_metadata();
