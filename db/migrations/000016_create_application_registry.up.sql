CREATE TABLE applications (
    id BIGSERIAL PRIMARY KEY CHECK (id > 0),
    public_id UUID NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    key VARCHAR(63) NOT NULL UNIQUE
        CHECK (key COLLATE "C" ~ '^[a-z][a-z0-9-]{1,61}[a-z0-9]$'),
    name TEXT NOT NULL
        CHECK (name = btrim(name) AND octet_length(name) BETWEEN 1 AND 128
               AND name !~ '[[:cntrl:]]' AND name !~ '^[[:space:]]|[[:space:]]$'),
    status TEXT NOT NULL DEFAULT 'PROVISIONING'
        CHECK (status = 'PROVISIONING'),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT current_timestamp,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT current_timestamp
);

CREATE TABLE application_staff_grants (
    application_id BIGINT NOT NULL REFERENCES applications(id) ON DELETE RESTRICT,
    staff_id BIGINT NOT NULL CHECK (staff_id > 0)
        REFERENCES staff_users(id) ON DELETE CASCADE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT current_timestamp,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT current_timestamp,
    PRIMARY KEY (application_id, staff_id)
);

CREATE INDEX idx_application_grants_staff_active
    ON application_staff_grants(staff_id, application_id) WHERE is_active;

ALTER TABLE audit_logs
    ADD COLUMN application_id BIGINT REFERENCES applications(id) ON DELETE RESTRICT,
    ADD CONSTRAINT audit_application_scope_required
        CHECK (entity_type NOT IN ('application', 'application_grant')
               OR application_id IS NOT NULL);

CREATE INDEX idx_audit_application_created
    ON audit_logs(application_id, created_at, id) WHERE application_id IS NOT NULL;
