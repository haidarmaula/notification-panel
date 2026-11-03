# M02b — Staff administration verification

Status: implemented and locally verified, 2026-11-03. Not deployed.
[SPEC](../specs/m02b-staff-administration.md),
[canonical permission/route matrix](../contracts/staff-permissions.json),
[API authorization](../api/authorization-api.md).

## Result

`staff.manage` is granted only to active current-DB SUPER_ADMIN accounts.
All seven existing staff routes and all seven public service methods check
independently. Server wiring no longer uses the JWT-role SuperAdminMiddleware.
The legacy SUPER_ADMIN_ROLE setting remains required for configuration
compatibility, but cannot change the policy. No schema or repository-interface
changes were needed; the existing GetStaffAuthority query is reused.

Password reset retains actor-password confirmation and the existing 8–72-byte
reset bounds. Its separate actor/hash lookup fails closed for missing, inactive,
mismatched or unavailable actors. Delete uses the authorized principal for its
self-deletion check. A private response-read helper preserves successful
self-demotion/deactivation responses and the existing audit call after the write.

The JSON `routes` array retains M02a's 14 notification/segment/member routes;
`staff_routes` adds seven staff routes. Tests read both against the same role
grants. The runtime uses the tested server-owned permission map, not editable JSON.

## Evidence

All checks below exited 0 with Go 1.26.10 and the repository's pinned modules:

| Check | What it establishes |
| --- | --- |
| `go test ./...` | Existing unit baseline plus 56 staff service denial cases, actor lookup failures, reset policy/success and self-delete tests; direct handler safe-error coverage expands to 21 handlers × 3 failure classes |
| `go build ./...` | All binaries and constructor callers compile |
| `go vet ./...` and `go vet -tags=integration ./tests/integration` | Default and tagged test static checks |
| `go test -race ./internal/authz/... ./internal/features/staff/...` | Focused local race checks |
| `bash scripts/ci.sh format` | Tracked Go formatting; newly added Go files also formatted with gofmt |
| `bash scripts/integration.sh` | Real local PostgreSQL/Kafka, migrations and SDK HTTP simulation; all existing and new tagged tests pass |

`TestStaffAdministrationAPI` uses the real staff module, API-key/JWT/audit chain,
repositories and PostgreSQL inside a rollback-only transaction. Its 28 cases
cover four roles × seven routes. Lower roles carry misleading SUPER_ADMIN token
claims; authorized SUPER_ADMIN requests carry VIEWER claims. Denied requests
preserve full staff/audit row snapshots. Public responses exclude password hashes.
Additional tests cover missing API key/JWT, account lifecycle, wrong reset
confirmation, self-delete, self-demotion/deactivation response and audit, and a
demotion between route and service checks. Nil business repositories in the last
case prove denial occurs before target access.

`TestCommittedStaffAuthority` uses pool operations that commit before each
request, reuses one signed JWT and verifies 200 → 403 after demotion → 401 after
deactivation/deletion. This is separate from rollback-fixture evidence.

Full integration test execution passed in 13.172 seconds, excluding environment
startup. Existing SDK simulation recorded nine actual fixture HTTP requests
(200 × 2, 404 × 1, 429 × 1, 503 × 5). No real FCM/APNs requests or workers ran.
The runner cleaned up its owned fixture resources and simulator on successful exit.

Local logs: `/tmp/hello-m02b-{unit,build,vet,integration-vet,race,format}.txt`
and `/tmp/hello-m02b-integration.txt`. These temporary logs are not repository artifacts.

## Remaining limits and next slice

M02 overall and production-plan step 20 remain in progress. Checks are statement
snapshots; an already-authorized operation can race subsequent authority changes.
There is no new session revocation or last-super-admin protection. Audit remains
best effort, writes/refetches remain non-atomic, and existing full repository
rows in staff Update/UpdateStatus/Delete Before snapshots still include
PasswordHash. Redaction needs a dedicated follow-up, including historical data
handling; the public-response assertions do not establish audit redaction.

Continue M02 with request/body limits and audit redaction, followed by the
remaining identity/session and API baseline contracts. No new vulnerability scan
was run for this slice (no dependency changes); the previous scan is historical
evidence. Hosted CI, production deployment, external auth/mobile integration,
real provider delivery, capacity, recovery and production readiness remain open.

Follow-up 2026-11-03: [M02c](m02c-verification.md) fixes new staff audit snapshots,
including the additional Create/After exposure found during implementation.
Historical cleanup is prepared/tested locally, not executed on production.
The limitations above describe the M02b checkpoint.
