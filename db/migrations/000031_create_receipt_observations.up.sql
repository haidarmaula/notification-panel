CREATE TABLE notification_receipt_requests (
    application_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    client_event_id UUID NOT NULL,
    delivery_id UUID NOT NULL,
    fingerprint BYTEA NOT NULL CHECK (octet_length(fingerprint)=32),
    expires_unix BIGINT NOT NULL CHECK (expires_unix>0),
    PRIMARY KEY(application_id,user_id,client_event_id),
    FOREIGN KEY(application_id,user_id) REFERENCES users(application_id,id) ON DELETE RESTRICT,
    FOREIGN KEY(application_id,delivery_id) REFERENCES notification_delivery_intents(application_id,id) ON DELETE RESTRICT
);
CREATE INDEX idx_receipt_request_expiry ON notification_receipt_requests(expires_unix);

CREATE TABLE notification_observations (
    application_id BIGINT NOT NULL,
    delivery_id UUID NOT NULL,
    event TEXT NOT NULL CHECK (event IN ('OPENED','DELIVERED')),
    received_at TIMESTAMPTZ NOT NULL,
    observed_at TIMESTAMPTZ,
    PRIMARY KEY(delivery_id,event),
    FOREIGN KEY(application_id,delivery_id) REFERENCES notification_delivery_intents(application_id,id) ON DELETE RESTRICT
);
