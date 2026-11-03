# Staff audit credential cleanup

M02c operator procedure, 2026-11-03. Prepared and tested locally; no company
server or production data has been accessed or changed by this work.

## Scope and deployment order

Legacy staff creation audits can contain PasswordHash in After; update/status/delete
can contain it in Before. M02c writes credential-free StaffSnapshot projections
and rejects raw staff payloads at AuditService. Reset audits remain metadata-only.
Deploy the fixed API binary to every writer first, including rollback candidates.
Check for older jobs, scripts, direct SQL writers and export consumers. This guard
is application-level, not a database constraint. Existing session/password/audit
durability behavior is unchanged.

Cleanup is an explicit data operation, not a schema migration or an API endpoint.
Have the authorized database/operator owner review the maintenance window, audit
integrity policy and this procedure. The script preserves event ID/action/actor,
entity metadata and original timestamp, and returns counts only. Record change
ticket, database identity, script/code revision, batch counts and final counts in
the operator record; do not copy old payloads into tickets or logs.

Both JSON columns for `staff_user` are projected to the flat scalar keys ID,
RoleID, RoleName, Name, Email, IsActive, CreatedAt, UpdatedAt. Unknown keys and
nested values are discarded, including legacy credential-name variants. Arrays,
scalars and JSON null become SQL NULL. Password reset snapshots become SQL NULL.
Non-staff events and already-clean rows are untouched. Name/email remain audit
PII, subject to the separate retention/access policy. Secrets stored under an
otherwise allowed scalar field cannot be detected by key-based projection;
unexpected legacy formats need owner review without dumping credential values.

## Local commands for an authorized operator

Run from the repository root. Use a DBA-provisioned libpq service called
`hello-audit-maintenance`, with credentials outside Git/command-line arguments
and appropriate restricted file permissions. The commands below are templates,
not an instruction to connect to an unverified environment. The application
itself does not acquire maintenance credentials.

First, establish the target identity and retention/restore plan with the DBA.
The existing secure backup policy still applies; do not create an extra plaintext
copy of audit snapshots for rollback. Avoid session/client settings that echo
rows or credential values. Count-only inventory (may scan the audit table):

```sh
PGSERVICE=hello-audit-maintenance psql -X -v ON_ERROR_STOP=1 \
  -c "SET statement_timeout = '30s';" \
  -f scripts/sql/count-unsafe-staff-audits.sql
```

Dry-run one batch inside a transaction and roll it back. This acquires row locks
briefly; it is not a read-only query. It must leave the unsafe count unchanged:

```sh
PGSERVICE=hello-audit-maintenance psql -X -v ON_ERROR_STOP=1 \
  -c "BEGIN; SET LOCAL lock_timeout = '1s'; SET LOCAL statement_timeout = '10s';" \
  -f scripts/sql/redact-staff-audit-batch.sql \
  -c 'ROLLBACK;'
```

After reviewing the count/result, apply one atomic batch:

```sh
PGSERVICE=hello-audit-maintenance psql -X -v ON_ERROR_STOP=1 --single-transaction \
  -c "SET LOCAL lock_timeout = '1s'; SET LOCAL statement_timeout = '10s';" \
  -f scripts/sql/redact-staff-audit-batch.sql
```

Repeat reviewed batches, monitoring database latency, lock contention, WAL volume
and replication lag; pause on errors/threshold breaches. LIMIT 500 bounds updates
and selected row locks, not total scan cost. Do not wrap all batches in one long
transaction. Timeout/error aborts that batch; investigate before retrying. No
original secrets are copied into a reverse script. A committed redaction cannot
be undone to recover credentials; reverting the application does not restore them.

## Completion and historical copies

SKIP LOCKED allows concurrent workers without waiting on the same row. A batch
returning zero can mean remaining rows are locked. Rerun the read-only unsafe
count, resolve/finish blocking transactions and repeat until the count is zero
with all old writers quiesced/upgraded. Confirm the count again after the write
window and verify representative metadata using safe keys/counts only.

Check replica replay/catch-up before verifying the same count on readable
replicas. UPDATE does not securely erase old MVCC tuples, WAL, physical backups,
PITR archives, snapshots, replicas, exports or copied logs. Keep those restricted,
apply the approved expiry/destruction policy, and include sanitization/zero-count
verification before a restored database is exposed to audit readers. Schedule
normal maintenance with the DBA; this procedure does not run VACUUM FULL or
rewrite storage. Existing restore/recovery release gates remain open.

If investigation confirms unauthorized exposure, the security owner must decide
incident handling and credential/session response; removing audit fields does
not itself invalidate a compromised password or token. Track production cleanup
and historical-copy disposition separately from local code/test completion.
