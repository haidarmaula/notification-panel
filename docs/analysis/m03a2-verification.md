# M03a.2 — Application resolver and services

2026-11-03. Implemented and locally verified; **no application HTTP routes or
server wiring yet**. [SPEC](../specs/m03a-application-registry.md).

## Result

The shared operation policy adds applications.read (four known roles) and
applications.manage (SUPER_ADMIN only), with the canonical JSON matrix kept in
sync. Existing notification/segment/staff route arrays and permissions remain.
A new required app authorizer reads principal, optional app and grant in one
query. It ignores JWT role claims, requires active current staff and explicit
app membership, denies a SUPER_ADMIN wildcard, and admits only metadata reads
for PROVISIONING. Unknown/foreign app scope is not-found; global staff/project
administration cannot be delegated through an app scope. No existing business
endpoint is retrofitted or made app-isolated by adding this checker.

The applications service implements ten registry/grant operations with current
DB guards. Management can inspect global registry metadata; granted metadata
reads remain separate. Create does not give its creator an implicit grant.
Names/keys/IDs/pagination and supported conditional tags are validated; HTTP
media/duplicate-field/body handling remains M03a.3. Renames and grant transitions
check resource-bound strong ETags, preserve matching no-op revisions/timestamps
and emit no no-op audit. Revocation is soft; regrant advances the old revision.

Real writes lock the app control parent first, recheck the actor using the same
transaction's queries, and commit safe allowlisted audit snapshots atomically.
New app insertion has no staff FK before its actor check. Audit actor derives
from the authenticated identity; entity/app scope follows the SPEC. No returned
mutation model leaks an uncommitted result on error. Target staff deletion after
lookup maps its specific FK failure to not-found and rolls back the grant.
A stale CAS yields precondition failure.

Transactions explicitly use READ COMMITTED. Only SQLSTATE 40001/40P01 retry the
whole transaction, at most three attempts with small context-aware backoff.
Other errors, including ambiguous transport commit failures, are not retried;
clients reconcile using the SPEC's key/GET/conditional-state rules. Permission
checks are statement snapshots, not a promise to cancel in-flight work after
revocation. No activation, provider, Kafka or credential dependency is introduced.

## Checks actually run

All final checks exited 0 with Go 1.26.10 and the existing pinned modules:

| Check | Evidence |
| --- | --- |
| `go test ./...`, `go build ./...`, `go vet ./...` | Full default baseline and all package/binary/static checks |
| `go vet -tags=integration ./tests/integration` | Tagged integration compilation/static checks |
| `go test -race ./internal/features/applications/... ./internal/authz/...` | Focused unit race checks; real-DB tests are not race-instrumented |
| `bash scripts/ci.sh format` | Tracked Go format; new files also gofmt-formatted |
| `bash scripts/integration.sh` | Existing 16-migration/dependency/security/audit/body suite plus real service tests |

Unit scope tests cover metadata/role permission, no implicit SUPER_ADMIN grant,
missing/revoked/foreign app, staff status/deletion, missing identity, DB failures,
inconsistent authority rows, unknown roles/permissions/status, cancellation and
fresh grant reload. They also reject global management permissions as app scope.
Eight management methods reject eight invalid/unauthorized actor conditions
before target/transaction I/O (64 combinations, with no permissive fake checker).
Contract tests cover UTF-8 byte/name/control limits and precondition forms.

Retry-boundary unit tests cover callback serialization/deadlock retries,
exhaustion after three attempts, serialization at commit (whole callback rerun),
cancellation and no automatic retry of an ambiguous commit. These use fake
transaction mechanics; they do not claim real PostgreSQL deadlock/connection-loss
fault injection or prove that rollback of an unknown commit can undo it.

TestApplicationServices uses committed PostgreSQL fixtures and real services.
All four roles read only granted project metadata; lower-role management calls
are forbidden without changing app/grant/audit snapshots. SUPER_ADMIN initially
has no business grant, then receives one explicitly. Its misleading VIEWER token
claim still permits management through current DB role; stale SUPER_ADMIN claims
after committed demotion do not. Create/rename/enable/revoke paths, no-ops,
stale/missing/invalid conditions, duplicate key and revoke/regrant ABA are tested.

Five audit insert faults (create, rename, new grant, revoke, reactivation) are
injected using an actor/action-scoped fixture CHECK. Each leaves the full
app/grant/audit snapshot unchanged. Safe before/after field sets, application
scope and actor attribution are inspected. Independent transactions racing on
one rename or first grant produce one success, one precondition failure and
one new audit. Inactive/deleted target cases fail without partial changes.
A real deletion after target lookup exercises the FK race path. A transactor
wrapper commits role demotion between precheck and transaction start, proving
the transaction recheck rejects/rolls back independently.

The final full integration execution passed in 14.391 seconds, excluding startup.
All committed registry/staff fixtures and failure constraints were cleaned by
tests; the runner cleaned owned Docker/simulation resources. The existing SDK
simulation made nine local fixture HTTP requests; none came from this registry
service, and no real FCM/APNs was contacted.
Logs: `/tmp/hello-m03a2-{focused,unit,build,vet,integration-vet,race,format}.txt`
and `/tmp/hello-m03a2-integration.txt`. No dependency/SQL/schema changes or new
mocked-interface regeneration; no fresh dependency vulnerability scan ran.

## Remaining work

M03a.3 must add ten routes/handlers, safe DTO/error mapping, strict new-request
validation/media/body controls, no-store headers and route/API regression tests.
The OpenAPI remains proposed/unimplemented at its HTTP boundary. The application
services/resolver are not wired into current server/worker/scheduler binaries.
No second-app user/device/campaign data, operational app or legacy migration was
introduced. M03b isolation/legacy reader-writer gates, M03c/d mobile contracts,
production G3/G5/G7 acceptance and remaining M02 release controls remain open.
