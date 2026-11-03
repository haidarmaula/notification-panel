CREATE TABLE mobile_origin_uses (
    application_public_id UUID NOT NULL REFERENCES applications(public_id) ON DELETE RESTRICT,
    issuer TEXT COLLATE "C" NOT NULL CHECK (octet_length(issuer) BETWEEN 1 AND 255),
    nonce_digest BYTEA NOT NULL CHECK (octet_length(nonce_digest) = 32),
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (application_public_id, issuer, nonce_digest)
);

CREATE INDEX idx_mobile_origin_uses_expiry ON mobile_origin_uses (expires_at);
