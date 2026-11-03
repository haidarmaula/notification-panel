# M02c — Staff audit redaction verification

Status: implemented and locally verified, 2026-11-03; not deployed.
[SPEC](../specs/m02c-staff-audit-redaction.md),
[operator runbook](../runbooks/staff-audit-redaction.md).

## Change and scope

Staff Create/After and Update/UpdateStatus/Delete Before passed complete sqlc
StaffUser rows, including PasswordHash, to audit. The feature now projects all
staff snapshots to the explicit `audit.StaffSnapshot` allowlist. Even substituted
audit collaborators receive safe projections. AuditService independently rejects
raw staff rows, maps, arbitrary types and custom marshalers before insert.
The error includes no supplied values. Password reset forbids snapshots and
remains metadata-only. Optional null staff pointers encode SQL NULL.

Safe field names, timestamp representation, event metadata and HTTP behavior
are preserved. Name/email remain intentional audit PII. Other entity encoding
is unchanged; this is not a global secret scrubber or an audit durability change.

No schema, generated-code, repository-interface or migration changes were needed.
Historical cleanup is deliberately separate from startup/migrations:

- `scripts/sql/redact-staff-audit-batch.sql` locks/updates up to 500 candidate rows
  in one statement, uses SKIP LOCKED, preserves event metadata and returns counts.
- `scripts/sql/count-unsafe-staff-audits.sql` independently counts remaining
  snapshots outside the same flat scalar allowlist, including reset payloads.
- The runbook covers dry-run rollback, committed batches, locks, verification,
  old writers, irreversible credential removal, replicas, backups and restores.

## Verification actually run

All of these exited 0 using the repository's Go 1.26.10/toolchain and modules:

| Check | Evidence |
| --- | --- |
| `go test ./internal/audit/... ./internal/features/staff/...` | Safe projection, rejected row/map/custom marshaler/string/array, metadata preservation, null behavior, reset rejection, encoder/repository failures and existing staff behavior |
| `go test ./...` and `go build ./...` | Full default unit baseline and binary compilation |
| `go vet ./...` and `go vet -tags=integration ./tests/integration` | Default and integration static checks |
| `go test -race ./internal/audit/... ./internal/features/staff/...` | Focused local race checks |
| `bash scripts/ci.sh format` | Tracked Go formatting; new files formatted with gofmt |
| `bash scripts/integration.sh` | Full isolated dependency/authorization suite plus the real-DB audit cases below |

The staff API matrix still passes all 28 role/route combinations. Each authorized
mutation is now checked to persist an audit row. All five staff action types are
present; both stored JSON columns contain only allowed keys, preserve useful
identity fields and exclude fixture password material. Reset columns are NULL.

`TestHistoricalStaffAuditRedaction` executes the actual operator SQL files in a
rollback-only PostgreSQL fixture. It covers both columns, legacy credential
aliases, unknown/nested fields, JSON/SQL null, arrays/scalars, safe rows, reset
clearing, non-staff preservation, metadata preservation and dry-run rollback.
A 503-row dirty fixture is processed as 500 then 3 then 0, with independent
remaining counts of 503 → 3 → 0 and idempotent repetition.

`TestStaffAuditRedactionSkipsLockedRows` holds a row lock on a separate
connection: one row is scrubbed, the next batch returns 0 while the count still
reports 1, and releasing the lock allows the final row to be scrubbed. It verifies
why zero updated rows alone is not a completion signal. Committed fixtures are
cleaned by the test.

Integration test execution passed in 13.302 seconds, excluding environment
startup. Existing local SDK simulation recorded nine fixture HTTP requests;
no real FCM/APNs or application worker was used. Runner cleanup succeeded.
SQL was tested against local PostgreSQL, not an actual company database. The
runbook's production libpq service/permissions remain unverified.

Local temporary logs: `/tmp/hello-m02c-{focused,unit,build,vet,integration-vet,race,format}.txt`
and `/tmp/hello-m02c-integration.txt`. No new dependency vulnerability scan was
run (no dependency changes); previous scans remain historical evidence.

## Remaining work

M02 and plan step 20 remain in progress. Next is M02d request/body limits and
reassessment of the remaining baseline. Atomic mutation/audit, session controls,
other entity payload schemas and production-readiness gates are separate work.
The application guard does not constrain direct SQL or older deployed binaries.
Production data has not been inspected or scrubbed, and UPDATE does not erase
old WAL/backups/exports. Operator completion and historical-copy handling must
be tracked separately before a production remediation claim is made.
