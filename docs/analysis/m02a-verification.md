# M02a permission verification

Tanggal: 2026-11-02. Status: implemented and locally verified in code; not deployed.
[SPEC](../specs/m02a-staff-operation-permissions.md),
[policy contract](../contracts/staff-permissions.json),
[API behavior](../api/authorization-api.md).

## Result and implementation

Notification, segment and member routes/services now require current active DB
authority. VIEWER cannot mutate; OPERATOR cannot send or persist non-null schedule
input; ADMIN/SUPER_ADMIN can, subject to existing domain guards. Unknown roles/
operations deny. JWT role claims are ignored on these gates. Creators are taken
from the authorized principal rather than supplied CreateParams.CreatedBy.

`internal/authz` owns the closed operation matrix, Require, route Guard and safe
401/403 mapping. `GetStaffAuthority` joins staff/role in one SQL statement and
does not read password hashes. Source query and regenerated sqlc are committed
together; no schema migration or mocked feature interface change was required.
Services require a checker constructor argument; module wiring installs the
actual DB authorizer. There is no production allow-all/nil fallback.

The three members-handler unknown-500 branches now use the literal
`internal server error`. Legacy success DTOs/statuses and DRAFT-only guards are
preserved. PATCH scheduling still only persists a timestamp; durable schedule
transition remains M04. Ordinary staff administration/mobile/operational gates
are outside this slice.

## Acceptance evidence

| SPEC cases | Evidence executed |
| --- | --- |
| A01 | Authz unit matrix matches the independent user-confirmed JSON contract; all roles/operations plus unknown/case variants |
| A02/A05/A06/A08/A10/A16 | In-process real ServeMux/API-key/JWT requests for 4 roles × all 14 routes (56 route/role cases), backed by actual PostgreSQL and Kafka fixtures; stale claim roles deliberately differ from DB |
| A03/A04/A14 | Missing/wrong/typed-nil/nonpositive identity, missing/inactive actor, unknown role, DB error, missing-role integrity and canceled/deadline checks; cancellation during lookup cannot return a principal |
| A07/A09 | Nine direct service mutations denied with nil domain collaborators; real-DB API and direct-service denials compare complete business-row snapshots and both Kafka end offsets; scheduling blocked through create/PATCH/send |
| A11 | OPERATOR create absent/null stays DRAFT; title PATCH with null preserves an existing scheduled timestamp |
| A12 | Direct notification/segment create with spoofed creator stores actual actor ID; corresponding audit actor matches |
| A13/A17 | Roles moved to IDs >100; current role still determines authority. Separate held authority connection sees committed demotion/offboarding from another pooled connection; demotion between route check and service check denies |
| A15 | All 14 handlers tested directly for 401/403/internal-500 mapping; wrapped denials and private error details handled safely |
| A18 | Build/default unit tests/vet and full tagged integration runner pass; authz/segments race tests pass |

Real-DB API fixture uses a rollback-only transaction for broad role/mutation
coverage; a separate autocommit test establishes cross-connection committed
authority visibility. API requests use local httptest recorders with real JWT
middleware, not a remotely hosted server. Authorized send tests publish only to
the disposable broker; no worker/provider/device executes those test events.
The Kafka smoke test now records its start offset so earlier API fixture events
cannot be mistaken for its own round-trip.

Executed checks: `go test ./...`, `go build ./...`, `go vet ./...`,
`go vet -tags=integration ./tests/integration`,
`go test -race ./internal/authz/... ./internal/features/segments/...`, and
`bash scripts/integration.sh`. Go cache/module cache used /tmp paths described
in the dependency readiness document. Integration runner cleaned its containers,
network and local simulation. Formatting/whitespace and local contract links
are checked before commit. The govulncheck gate also passed with 0 reachable findings; 1 imported-package
and 3 required-module advisories remained informational/unreachable according
to that scan. No general security sign-off is inferred.

## Limits and next slice

C21's notification/segment/member operation bypass is fixed in this local code
and test scope. This is not a full security sign-off. Authority checked at a
statement snapshot is not locked through a later mutation; transactional
revocation/mutation/audit coupling and concurrency proof remain with M03/M04.
Legacy staff administration role checks, session invalidation, body/rate/DTO
hardening and gateway/BFF boundaries remain M02 follow-up/release gates.

No Linux/hosted CI, deployment, real Firebase/APNs/mobile send or production
capacity evidence was produced by M02a. ADR-11 overall remains proposed. Next:
write/implement the next small M02 security/API slice, reusing this checker;
step 20 remains ongoing per milestone rather than declaring every SPEC written.
