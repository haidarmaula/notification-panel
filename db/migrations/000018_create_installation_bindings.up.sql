CREATE TABLE mobile_installations (
    id BIGSERIAL PRIMARY KEY CHECK (id > 0),
    public_id UUID NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    application_id BIGINT NOT NULL REFERENCES applications(id) ON DELETE RESTRICT,
    owner_user_id BIGINT,
    proof_verifier BYTEA NOT NULL CHECK (octet_length(proof_verifier) = 32),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    owner_epoch BIGINT NOT NULL DEFAULT 1 CHECK (owner_epoch > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (application_id, id),
    UNIQUE (application_id, id, owner_user_id, owner_epoch),
    FOREIGN KEY (application_id, owner_user_id)
        REFERENCES users(application_id, id) ON DELETE RESTRICT
);

CREATE TABLE installation_bindings (
    id BIGSERIAL PRIMARY KEY CHECK (id > 0),
    application_id BIGINT NOT NULL,
    installation_id BIGINT NOT NULL,
    owner_user_id BIGINT NOT NULL,
    owner_epoch BIGINT NOT NULL CHECK (owner_epoch > 0),
    platform TEXT NOT NULL CHECK (platform IN ('ANDROID', 'IOS')),
    provider_namespace TEXT COLLATE "C" NOT NULL CHECK (octet_length(provider_namespace) BETWEEN 1 AND 255),
    provider_route_key TEXT NOT NULL CHECK (octet_length(provider_route_key) BETWEEN 1 AND 255),
    address_type TEXT NOT NULL CHECK (address_type IN ('FCM_FID', 'FCM_REGISTRATION_TOKEN')),
    address_fingerprint BYTEA NOT NULL CHECK (octet_length(address_fingerprint) = 32),
    address_ciphertext BYTEA NOT NULL CHECK (octet_length(address_ciphertext) > 0),
    encryption_key_version INTEGER NOT NULL CHECK (encryption_key_version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    retired_at TIMESTAMPTZ CHECK (retired_at >= created_at),
    active_owner_user_id BIGINT GENERATED ALWAYS AS
        (CASE WHEN retired_at IS NULL THEN owner_user_id END) STORED,
    active_owner_epoch BIGINT GENERATED ALWAYS AS
        (CASE WHEN retired_at IS NULL THEN owner_epoch END) STORED,
    UNIQUE (application_id, id),
    FOREIGN KEY (application_id, installation_id)
        REFERENCES mobile_installations(application_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (application_id, owner_user_id)
        REFERENCES users(application_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_binding_current_owner
        FOREIGN KEY (application_id, installation_id, active_owner_user_id, active_owner_epoch)
        REFERENCES mobile_installations(application_id, id, owner_user_id, owner_epoch)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX uq_active_installation_binding
    ON installation_bindings (installation_id) WHERE retired_at IS NULL;
CREATE UNIQUE INDEX uq_active_provider_address
    ON installation_bindings (provider_namespace, address_type, address_fingerprint)
    WHERE retired_at IS NULL;
CREATE INDEX idx_installation_binding_history
    ON installation_bindings (application_id, installation_id, id);

CREATE FUNCTION guard_mobile_installation_update() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF ROW(NEW.id, NEW.public_id, NEW.application_id, NEW.proof_verifier, NEW.created_at)
        IS DISTINCT FROM ROW(OLD.id, OLD.public_id, OLD.application_id, OLD.proof_verifier, OLD.created_at)
       OR NEW.version < OLD.version
       OR (NEW.owner_user_id IS DISTINCT FROM OLD.owner_user_id
           AND (NEW.owner_epoch <> OLD.owner_epoch + 1 OR NEW.version <= OLD.version))
       OR (NEW.owner_user_id IS NOT DISTINCT FROM OLD.owner_user_id AND NEW.owner_epoch <> OLD.owner_epoch)
    THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'invalid installation identity or version change';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER guard_mobile_installation_update BEFORE UPDATE ON mobile_installations
FOR EACH ROW EXECUTE FUNCTION guard_mobile_installation_update();

CREATE FUNCTION guard_installation_binding_update() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF ROW(NEW.id, NEW.application_id, NEW.installation_id, NEW.owner_user_id, NEW.owner_epoch,
           NEW.platform, NEW.provider_namespace, NEW.provider_route_key, NEW.address_type,
           NEW.address_fingerprint, NEW.address_ciphertext, NEW.encryption_key_version, NEW.created_at)
       IS DISTINCT FROM
       ROW(OLD.id, OLD.application_id, OLD.installation_id, OLD.owner_user_id, OLD.owner_epoch,
           OLD.platform, OLD.provider_namespace, OLD.provider_route_key, OLD.address_type,
           OLD.address_fingerprint, OLD.address_ciphertext, OLD.encryption_key_version, OLD.created_at)
       OR (OLD.retired_at IS NOT NULL AND NEW.retired_at IS DISTINCT FROM OLD.retired_at)
    THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'binding generation is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER guard_installation_binding_update BEFORE UPDATE ON installation_bindings
FOR EACH ROW EXECUTE FUNCTION guard_installation_binding_update();
