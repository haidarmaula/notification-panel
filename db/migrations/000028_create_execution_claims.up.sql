ALTER TABLE notifications
    ADD COLUMN lease_owner UUID,
    ADD COLUMN lease_generation BIGINT NOT NULL DEFAULT 0 CHECK (lease_generation>=0),
    ADD COLUMN lease_until TIMESTAMPTZ,
    ADD CONSTRAINT execution_lease_pair CHECK ((lease_owner IS NULL)=(lease_until IS NULL));

ALTER TABLE notification_delivery_intents
    ADD COLUMN attempt_id UUID,
    ADD COLUMN claim_generation BIGINT,
    ADD COLUMN last_attempt_at TIMESTAMPTZ,
    ADD COLUMN attempt_resolved_at TIMESTAMPTZ,
    ADD CONSTRAINT delivery_attempt_pair CHECK (
        (attempt_id IS NULL AND claim_generation IS NULL AND last_attempt_at IS NULL AND attempt_resolved_at IS NULL)
        OR (attempt_id IS NOT NULL AND claim_generation IS NOT NULL AND claim_generation>0 AND last_attempt_at IS NOT NULL
            AND attempt_count>0 AND (attempt_resolved_at IS NULL OR attempt_resolved_at>=last_attempt_at))
    );
CREATE UNIQUE INDEX uq_delivery_attempt_id ON notification_delivery_intents(attempt_id) WHERE attempt_id IS NOT NULL;
CREATE INDEX idx_execution_lease_recovery ON notifications(lease_until,id)
    WHERE status='PROCESSING' AND deadline_at IS NOT NULL;
