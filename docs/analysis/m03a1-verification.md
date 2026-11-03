# M03a.1 — Registry persistence verification

2026-11-03. Implemented and locally verified: schema, queries/generated sqlc,
repository and optional audit scope. **No application HTTP/service guard is wired.**
[SPEC](../specs/m03a-application-registry.md), [sequence](../specs/m03a-application-scope-plan.md).

## Change

Paired migration 000016 creates applications and application_staff_grants,
adds nullable audit_logs.application_id and enforces scope for the new registry
entity labels. Registry has opaque UUID identity, unique ASCII key (C collation),
trimmed/control-free/128-byte name constraints, positive revisions/IDs and only
PROVISIONING status. Grant PK/FKs, staff-deletion cascade and retained app/audit
FKs match the SPEC. It inserts no apps, grants, users or mappings on migration.

Queries/repository support create/get/row lock, revision CAS rename, grant create/
read/CAS, authority-data lookup and scoped membership lists. Count and JSON page
come from one SQL statement, including empty/out-of-range pages. JSON projection
is explicit; repository decoding preserves typed IDs and timestamp instants.
No activation, delete or identity-reassignment repository methods are exposed.
This repository does not authenticate or authorize callers; services must do so.

Audit LogParams gains optional positive ApplicationID (0 => SQL NULL, negative
rejected before insert). Create/get/list queries and generated models carry scope.
New registry action/entity values are registered; existing values remain unchanged.
Current legacy audit callers continue to write NULL. The CreateAuditLog returning
projection matches the complete generated AuditLog row, preserving its repository
return interface. sqlc was generated from SQL, never hand-edited.

## Checks and evidence

All final checks exited 0 using Go 1.26.10 and existing pinned dependencies:

| Check | What it establishes |
| --- | --- |
| `go test ./internal/audit/... ./internal/database/repository/...` | Shared audit compatibility; new persistence compiles |
| `go test ./...`, `go build ./...`, `go vet ./...` | Full default unit/build/static baseline |
| `go vet -tags=integration ./tests/integration` | Tagged test static validation |
| `go test -race ./internal/audit/... ./internal/database/repository/...` | Focused default-tag race checks; real-DB concurrency is separately tested below, not race-instrumented |
| `bash scripts/ci.sh format` | Tracked Go formatting; new files also gofmt-formatted |
| `sqlc generate -f db/sqlc.yaml` | Generated models/interfaces/queries; a second generation had identical hashes |
| `bash scripts/integration.sh` | All 16 migrations up/down-all/up and the full existing/new fixture suite |

TestApplicationRegistryPersistence uses transaction-bound real repositories and
PostgreSQL to verify UUID/defaults/identity-preserving rename, list filters/counts/
empty pages, membership inclusion/revocation, nullable missing app/grant authority,
current role/active-state data, soft revoke/regrant revision 1→2→3 and stale/no-op
CAS. It tests duplicate/invalid keys, names (including UTF-8 byte boundaries,
control and Unicode whitespace), operational status denial, IDs/revisions, grant
uniqueness and all relevant FK rejection/cascade behavior. Audit create/get/list
preserves scoped IDs and legacy NULL; grant audit facts survive target deletion.

A nested caller transaction deliberately fails its audit scope CHECK and rolls
back its registry/grant writes. This demonstrates transaction plumbing, not an
implemented application service's atomicity guarantee; that service is M03a.2.

TestApplicationRegistryLockAndCAS uses independent pool connections: a second
parent lock times out while the first holds it. Two concurrent rename attempts
with revision 1 produce one success and one pgx.ErrNoRows; final revision is 2.

TestApplicationRegistryMigrationRoundTrip executes the actual up/down SQL inside
a fixture transaction. An old global audit row survives with unchanged metadata
and new NULL scope. Repeated down/up works. A preexisting unscoped `application`
audit entity causes the new CHECK to reject migration rather than guess ownership.
This fixture rollback does not model production downgrade approval or data recovery.

The final full integration run passed in 14.248 seconds, excluding startup.
Initial test comparisons were corrected to compare timestamp instants: pgx binary
rows and JSON page rows can use different time.Location representations for the
same instant. No timestamp data fix was needed. The existing SDK simulation remains
fixture-only; no real provider was contacted. Owned fixture resources were cleaned.
Logs: `/tmp/hello-m03a1-{focused,unit,build,vet,integration-vet,race,format}.txt`
and `/tmp/hello-m03a1-integration.txt`. No new dependency scan ran; earlier scans
are historical evidence. No dependency or mocked feature-interface changes.

## Deployment/rollback boundary and next work

Apply 000016 before deploying a binary using the new audit insert/read queries;
new queries against schema 000015 can fail and legacy best-effort audit callers
could otherwise lose audit records. Old binaries' explicitly projected legacy
audit queries remain compatible with the nullable additive column.

Before any real migration, inspect only the count of preexisting audit labels
`application` / `application_grant` (e.g. SELECT count(*) FROM audit_logs WHERE
entity_type IN ('application','application_grant')). If nonzero, stop and resolve
provenance with the operator; do not silently relabel persisted audit history or
invent an app mapping. Migration failure/dirty state requires operator review;
never force a version or discard data to bypass it. No production DB was inspected.

Down removes registry/grants/audit scope and is fixture/unreleased rollback only;
production downgrade requires a coordinated data-aware plan. Public API contracts
remain proposed. No app grants grant runtime access through old services, no
second-app business rows/backfill were introduced and no multi-app isolation claim
is made. Next is M03a.2 required resolver/current-authority services, conditional
semantics and atomic audit; then M03a.3 HTTP/strict input and role/API tests.
