# M02c — Staff audit credential exclusion

Contract established 2026-11-03. Evidence/status: [verification](../analysis/m02c-verification.md).

## New writes

Every staff create/update/status/delete snapshot uses a dedicated `audit.StaffSnapshot`
allowlist: ID, RoleID, optional RoleName, Name, Email, IsActive, CreatedAt, UpdatedAt.
Existing Go-style JSON field names and scalar timestamp encoding are retained.
Staff entity/action strings, actor attribution, event metadata, HTTP statuses and
response DTOs do not change. Name/email remain intentional audit PII; this is
credential exclusion, not anonymization.

AuditService accepts only this concrete snapshot type (or a pointer) for a non-null
staff snapshot. Raw repository rows, maps and arbitrary marshalers fail before
the insert; errors contain no supplied payload. Password reset remains metadata-only:
any non-null snapshot is rejected. Other entity encoding remains unchanged.
Staff feature projects at the source, so even a substituted audit collaborator
receives no credential-bearing row. SQL NULL before/after semantics remain.

## Historical data

Provide a separate operator SQL batch, not an automatic startup/schema migration.
It locks at most 500 candidate rows with SKIP LOCKED, sanitizes both staff JSON
columns to the same flat scalar allowlist, and clears reset snapshots. Unknown
keys and nested values are discarded; malformed/non-object snapshots become SQL
NULL. Event ID, action, actor, entity, timestamp and other metadata are preserved.
Clean rows and non-staff events are unchanged. Repeating a batch is safe.

Only counts may be returned; no credentials or full old snapshots in output.
No original sensitive payload is copied to a rollback table. Rollback before
commit is supported; committed deletion cannot reconstruct the secret.
The runbook must address old writers, locks, verification, replicas, WAL/backups,
exports and restricted handling of historical copies. Local testing does not
establish that production/history has been remediated.

## Verification

- Projection and AuditService rejection tests, including errors with no payload.
- Persisted real-DB create/update/status/delete audits have only allowed fields;
  password reset has null snapshots; metadata and useful changes remain.
- Historical fixtures cover both sides, password/case variants, unknown/nested
  fields, JSON/SQL null, arrays/scalars, non-staff preservation, batch boundary,
  locked rows, rollback and idempotency.
- Existing authorization/reset behavior remains green. Audit still best effort;
  atomic mutation/audit durability and other entity schemas remain separate work.
