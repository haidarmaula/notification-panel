-- Synthetic setup only. Seed previously approved, exclusive FCM cohort decisions
-- in the disposable schema. The dedicated route control test exercises issuance.
INSERT INTO binding_route_grants(id,application_id,binding_id,operator_role,origin_epoch,source_epoch,
    expected_revision,destination,cohort_id,evidence_id,reason,issued_at,expires_at,applied_at)
SELECT gen_random_uuid(),b.application_id,b.id,'pg_database_owner'::regrole::oid,r.origin_epoch,r.source_epoch,
    0,'FCM_ACTIVE','7a000000-0000-4000-8000-000000000001',gen_random_uuid(),'pilot',
    statement_timestamp(),statement_timestamp()+interval '1 hour',statement_timestamp()
FROM installation_bindings b CROSS JOIN notification_runtime r
WHERE r.singleton AND b.application_id=$1 AND b.retired_at IS NULL
    AND NOT EXISTS(SELECT 1 FROM binding_route_grants g WHERE g.binding_id=b.id AND g.application_id=b.application_id AND g.applied_at IS NOT NULL);
