CREATE TABLE mobile_identities (
    application_id BIGINT NOT NULL,
    identity_namespace TEXT COLLATE "C" NOT NULL CHECK (octet_length(identity_namespace) BETWEEN 1 AND 255),
    subject TEXT COLLATE "C" NOT NULL CHECK (octet_length(subject) BETWEEN 1 AND 255),
    user_id BIGINT NOT NULL,
    PRIMARY KEY (application_id, identity_namespace, subject),
    FOREIGN KEY (application_id, user_id) REFERENCES users(application_id, id) ON DELETE RESTRICT
);
