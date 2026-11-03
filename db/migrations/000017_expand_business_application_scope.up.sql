-- Additive ownership expansion only. NULL legacy rows remain unisolated.

-- No backfill/default/activation or relaxation of global subject/token uniqueness.

ALTER TABLE users ADD COLUMN application_id BIGINT
    CONSTRAINT fk_users_application REFERENCES applications(id) ON DELETE RESTRICT;

CREATE INDEX idx_users_application ON users(application_id) WHERE application_id IS NOT NULL;

ALTER TABLE device_tokens ADD COLUMN application_id BIGINT
    CONSTRAINT fk_device_tokens_application REFERENCES applications(id) ON DELETE RESTRICT;

CREATE INDEX idx_device_tokens_application ON device_tokens(application_id) WHERE application_id IS NOT NULL;

ALTER TABLE templates ADD COLUMN application_id BIGINT
    CONSTRAINT fk_templates_application REFERENCES applications(id) ON DELETE RESTRICT;

CREATE INDEX idx_templates_application ON templates(application_id) WHERE application_id IS NOT NULL;

ALTER TABLE segments ADD COLUMN application_id BIGINT
    CONSTRAINT fk_segments_application REFERENCES applications(id) ON DELETE RESTRICT;

CREATE INDEX idx_segments_application ON segments(application_id) WHERE application_id IS NOT NULL;

ALTER TABLE segment_members ADD COLUMN application_id BIGINT
    CONSTRAINT fk_segment_members_application REFERENCES applications(id) ON DELETE RESTRICT;

CREATE INDEX idx_segment_members_application ON segment_members(application_id) WHERE application_id IS NOT NULL;

ALTER TABLE notifications ADD COLUMN application_id BIGINT
    CONSTRAINT fk_notifications_application REFERENCES applications(id) ON DELETE RESTRICT;

CREATE INDEX idx_notifications_application ON notifications(application_id) WHERE application_id IS NOT NULL;

ALTER TABLE upload_batches ADD COLUMN application_id BIGINT
    CONSTRAINT fk_upload_batches_application REFERENCES applications(id) ON DELETE RESTRICT;

CREATE INDEX idx_upload_batches_application ON upload_batches(application_id) WHERE application_id IS NOT NULL;

ALTER TABLE upload_batch_rows ADD COLUMN application_id BIGINT
    CONSTRAINT fk_upload_batch_rows_application REFERENCES applications(id) ON DELETE RESTRICT;

CREATE INDEX idx_upload_batch_rows_application ON upload_batch_rows(application_id) WHERE application_id IS NOT NULL;

ALTER TABLE notification_targets ADD COLUMN application_id BIGINT
    CONSTRAINT fk_notification_targets_application REFERENCES applications(id) ON DELETE RESTRICT;

CREATE INDEX idx_notification_targets_application ON notification_targets(application_id) WHERE application_id IS NOT NULL;

ALTER TABLE notification_deliveries ADD COLUMN application_id BIGINT
    CONSTRAINT fk_notification_deliveries_application REFERENCES applications(id) ON DELETE RESTRICT;

CREATE INDEX idx_notification_deliveries_application ON notification_deliveries(application_id) WHERE application_id IS NOT NULL;

ALTER TABLE notification_reads ADD COLUMN application_id BIGINT
    CONSTRAINT fk_notification_reads_application REFERENCES applications(id) ON DELETE RESTRICT;

CREATE INDEX idx_notification_reads_application ON notification_reads(application_id) WHERE application_id IS NOT NULL;

ALTER TABLE users ADD CONSTRAINT uq_users_application_id UNIQUE(application_id, id);

ALTER TABLE device_tokens ADD CONSTRAINT uq_device_tokens_application_id UNIQUE(application_id, id);

ALTER TABLE templates ADD CONSTRAINT uq_templates_application_id UNIQUE(application_id, id);

ALTER TABLE segments ADD CONSTRAINT uq_segments_application_id UNIQUE(application_id, id);

ALTER TABLE notifications ADD CONSTRAINT uq_notifications_application_id UNIQUE(application_id, id);

ALTER TABLE upload_batches ADD CONSTRAINT uq_upload_batches_application_id UNIQUE(application_id, id);

ALTER TABLE device_tokens ADD CONSTRAINT uq_device_application_id_user UNIQUE(application_id, id, user_id);

ALTER TABLE device_tokens ADD CONSTRAINT fk_scope_device_tokens_user_id
    FOREIGN KEY(application_id, user_id) REFERENCES users(application_id, id) ON DELETE CASCADE;

ALTER TABLE segment_members ADD CONSTRAINT fk_scope_segment_members_segment_id
    FOREIGN KEY(application_id, segment_id) REFERENCES segments(application_id, id) ON DELETE CASCADE;

ALTER TABLE segment_members ADD CONSTRAINT fk_scope_segment_members_user_id
    FOREIGN KEY(application_id, user_id) REFERENCES users(application_id, id) ON DELETE CASCADE;

ALTER TABLE notifications ADD CONSTRAINT fk_scope_notifications_template_id
    FOREIGN KEY(application_id, template_id) REFERENCES templates(application_id, id) ON DELETE SET NULL (template_id);

ALTER TABLE upload_batch_rows ADD CONSTRAINT fk_scope_upload_batch_rows_batch_id
    FOREIGN KEY(application_id, batch_id) REFERENCES upload_batches(application_id, id) ON DELETE CASCADE;

ALTER TABLE notification_targets ADD CONSTRAINT fk_scope_notification_targets_notification_id
    FOREIGN KEY(application_id, notification_id) REFERENCES notifications(application_id, id) ON DELETE CASCADE;

ALTER TABLE notification_targets ADD CONSTRAINT fk_scope_notification_targets_segment_id
    FOREIGN KEY(application_id, segment_id) REFERENCES segments(application_id, id) ON DELETE CASCADE;

ALTER TABLE notification_targets ADD CONSTRAINT fk_scope_notification_targets_user_id
    FOREIGN KEY(application_id, user_id) REFERENCES users(application_id, id) ON DELETE CASCADE;

ALTER TABLE notification_targets ADD CONSTRAINT fk_scope_notification_targets_upload_batch_id
    FOREIGN KEY(application_id, upload_batch_id) REFERENCES upload_batches(application_id, id) ON DELETE CASCADE;

ALTER TABLE notification_deliveries ADD CONSTRAINT fk_scope_notification_deliveries_notification_id
    FOREIGN KEY(application_id, notification_id) REFERENCES notifications(application_id, id) ON DELETE CASCADE;

ALTER TABLE notification_deliveries ADD CONSTRAINT fk_scope_notification_deliveries_user_id
    FOREIGN KEY(application_id, user_id) REFERENCES users(application_id, id) ON DELETE CASCADE;

ALTER TABLE notification_reads ADD CONSTRAINT fk_scope_notification_reads_notification_id
    FOREIGN KEY(application_id, notification_id) REFERENCES notifications(application_id, id) ON DELETE CASCADE;

ALTER TABLE notification_reads ADD CONSTRAINT fk_scope_notification_reads_user_id
    FOREIGN KEY(application_id, user_id) REFERENCES users(application_id, id) ON DELETE CASCADE;

ALTER TABLE notification_deliveries ADD CONSTRAINT fk_scope_delivery_device_user
    FOREIGN KEY(application_id, device_token_id, user_id)
    REFERENCES device_tokens(application_id, id, user_id) ON DELETE CASCADE;

-- Ordinary DML may claim a NULL legacy owner once, but never clear/move it.
-- This is not authorization: runtime roles must not own/alter tables or triggers.
CREATE FUNCTION prevent_business_application_reassignment() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF OLD.application_id IS NOT NULL AND NEW.application_id IS DISTINCT FROM OLD.application_id THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'application ownership is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER immutable_application_owner BEFORE UPDATE OF application_id ON users
    FOR EACH ROW EXECUTE FUNCTION prevent_business_application_reassignment();

CREATE TRIGGER immutable_application_owner BEFORE UPDATE OF application_id ON device_tokens
    FOR EACH ROW EXECUTE FUNCTION prevent_business_application_reassignment();

CREATE TRIGGER immutable_application_owner BEFORE UPDATE OF application_id ON templates
    FOR EACH ROW EXECUTE FUNCTION prevent_business_application_reassignment();

CREATE TRIGGER immutable_application_owner BEFORE UPDATE OF application_id ON segments
    FOR EACH ROW EXECUTE FUNCTION prevent_business_application_reassignment();

CREATE TRIGGER immutable_application_owner BEFORE UPDATE OF application_id ON segment_members
    FOR EACH ROW EXECUTE FUNCTION prevent_business_application_reassignment();

CREATE TRIGGER immutable_application_owner BEFORE UPDATE OF application_id ON notifications
    FOR EACH ROW EXECUTE FUNCTION prevent_business_application_reassignment();

CREATE TRIGGER immutable_application_owner BEFORE UPDATE OF application_id ON upload_batches
    FOR EACH ROW EXECUTE FUNCTION prevent_business_application_reassignment();

CREATE TRIGGER immutable_application_owner BEFORE UPDATE OF application_id ON upload_batch_rows
    FOR EACH ROW EXECUTE FUNCTION prevent_business_application_reassignment();

CREATE TRIGGER immutable_application_owner BEFORE UPDATE OF application_id ON notification_targets
    FOR EACH ROW EXECUTE FUNCTION prevent_business_application_reassignment();

CREATE TRIGGER immutable_application_owner BEFORE UPDATE OF application_id ON notification_deliveries
    FOR EACH ROW EXECUTE FUNCTION prevent_business_application_reassignment();

CREATE TRIGGER immutable_application_owner BEFORE UPDATE OF application_id ON notification_reads
    FOR EACH ROW EXECUTE FUNCTION prevent_business_application_reassignment();
