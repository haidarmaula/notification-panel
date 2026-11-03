-- Fixture/unreleased rollback only: removes registry, grants and audit context.
ALTER TABLE audit_logs DROP CONSTRAINT audit_application_scope_required;
DROP INDEX idx_audit_application_created;
ALTER TABLE audit_logs DROP COLUMN application_id;
DROP TABLE application_staff_grants;
DROP TABLE applications;
