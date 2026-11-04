# SPEC M02a — Staff operation permissions

Date: 2026-11-02. Status: implemented and locally verified for M02a;
[acceptance evidence](../analysis/m02a-verification.md). Not deployed.
Role matrix confirmed by the user; ADR-11 as a whole remains proposed and
other session/enrollment/recovery/security gates are not automatically accepted.

Sources: [ADR-11 A-4](../adr/adr-11-security-baseline.md),
[ADR-09](../adr/adr-09-api-consistency.md),
[CR1–CR9](../analysis/adr-01-11-consistency-review.md),
[machine-readable policy](../contracts/staff-permissions.json).

## 1. Scope and existing evidence

M02a closes operation permission bypass C21 in notification, segment, and
segment-member routes/services. It also checks identity/active status for
those read paths. The policy JSON is the matrix/route-map source for review
and test fixtures; it is not runtime configuration clients can change.
HTTP auth remains API key + staff access JWT. Existing success DTOs/status/state guards
are not replaced by this slice. There are no new endpoints, schema
migrations, permission-management UI, or automatic provider sends.

| Pre-implementation evidence | Historical condition |
| --- | --- |
| `cmd/server/main.go`, both `routes.go` files | API-key/JWT/audit chain without operation-role gates for 14 routes |
| `notifications/service.go`: Create/Update/Delete/Send | Reads actor row without explicit role or IsActive checks; JWT middleware checks active status in HTTP, does not protect direct service callers |
| `segments/service.go`: Create/Update/Delete | Similar actor lookup pattern; interface mocks available |
| `segments/members_service.go`: AddMember/RemoveMember | Does not check staff actor/permission; uses concrete repository |
| `db/queries/staff_users.sql`: GetStaffUserByID | Does not filter is_active; finding a row does not prove an active account |
| `middleware/super_admin.go` | Legacy gate uses JWT RoleName; not a current-authority gate for this feature |
| Notification CreateParams and segment CreateParams | CreatedBy can differ from actor on direct service calls |
| `members_handler.go` | Three unknown-500 branches still send err.Error(); must be safe when authorization errors are added |
| `notifications.Update` and UpdateNotification SQL | PATCH scheduled_at changes timestamp, but does not change DRAFT status to SCHEDULED |

The table above is a pre-implementation snapshot. C21 is now remediated by
code/tests, not by writing the SPEC; see evidence. NEW below marks
artifacts added by this slice.

## 2. Confirmed policy and route map

| Operation | VIEWER | OPERATOR | ADMIN | SUPER_ADMIN |
| --- | --- | --- | --- | --- |
| Existing permitted notification/segment/member reads | Yes | Yes | Yes | Yes |
| Notification draft create/edit/delete | No | Yes | Yes | Yes |
| Segment and membership create/edit/delete | No | Yes | Yes | Yes |
| Send and persist non-null scheduling input | No | No | Yes | Yes |
| Staff/role administration (target policy, outside first slice) | No | No | No | Yes |

The matrix applies to the existing shared admin resource scope. CreatedBy is
actor provenance, not an added owner-only ACL or tenancy. The slice does not
expand business reads or change data-redaction policy. If team/tenant/creator-specific
visibility is needed, it requires a separate object-scope contract
and query filters before that scope is enabled.

The full 14-method/path map is in the policy JSON. Required endpoint permissions
and field permissions are **AND**: notification create/update with
non-nil ScheduledAt requires draft.write **and** schedule. New roles/operations,
unknown names, different casing, empty role rows, or failed
lookups never grant permission. Do not assume role ID 1 is always
SUPER_ADMIN or trust the RoleName still stored in the JWT.
HEAD inherits GET/read permission through the existing ServeMux; unsupported
methods retain existing 404/405 behavior rather than gaining new permission.

## 3. Current-authority contract

The NEW internal authorization component accepts verified staff identity from
context and an operation identifier set by server code. It reads
actor ID, IsActive, and current role name through one SQL statement snapshot,
rather than using role claims or two lookups that could mix versions.
The NEW query is in `db/queries/staff_users.sql`, followed by sqlc regeneration; there
are no new columns, so no schema migration is required.

The returned principal contains only the ID and current role needed; do not
add password hashes/email to authorization DTOs/caches/logs. Missing actor,
nonpositive ID, wrong context type/typed-nil claims, nonexistent or inactive actor produce
an unauthenticated denial. An existing unknown role produces a forbidden denial.
DB/role integrity failures produce wrapped internal errors, not
errors disguised as invalid credentials or 404s. Cancellation/deadline does not
produce successful authorization or background mutation.

The concrete components are internal/authz.Authorizer, Checker.Require, and
AuthorityRepository.GetStaffAuthority. All services depend on the
Checker interface to test decisions without PostgreSQL.
Production implementation follows existing repository/sqlc conventions; module
constructors must wire a real checker, without a nil/allow-all fallback.

The route guard denies base operations before handler business I/O. Services still
check current authority on entry, including calls without HTTP. A route
decision/context cache is not authorization to bypass the service check.
Demotion/offboarding committed before the service statement is visible to
that statement, even if the token still carries the old role. Promotion also uses the latest
DB role, without requiring a role claim refresh to pass this feature's gate.

**Race boundary:** a statement-time check does not yet hold a principal/policy lock until
the mutation commits. This slice does not promise cancellation of an already-authorized
operation when a subsequent revocation commits. Atomic current-authority checks,
mutation/audit, and concurrency tests follow the data-owning M03/M04 transactions
and CR1 protocol; ADR-11/production acceptance still awaits those. The checker must
support transaction-bound queries, without per-request mutation of shared pool/global
state. Do not introduce a service↔outbox dependency cycle merely
to wire the first permission gate.

## 4. Privileged inputs and write boundaries

- **Create notification:** nil ScheduledAt takes the draft path; a non-nil
  timestamp requires schedule permission before target/template lookup and
  insert. Past timestamps remain validation errors for authorized actors.
- **PATCH notification:** non-nil ScheduledAt also requires schedule, even though
  the current service only stores the timestamp. Do not silently fix the state transition
  in the permission slice. Durable scheduling/deadline semantics
  belong to M04. Existing DRAFT-only edit/delete/send guards remain required.
- **Null/omission compatibility:** v1 pointers treat absent/null as
  nil. Create null remains a draft; PATCH null remains a no-op for that field,
  rather than clearing/cancelling the schedule. Null is not a bypass for storing
  timestamps. Presence-aware merge PATCH/clear behavior belongs to another SPEC.
- **Creator provenance:** notification/segment create uses the authorized
  principal's ID. Handler/client/internal CreateParams.CreatedBy must not
  forge the actor; the old field may be retained for source compatibility,
  but is not authority. Audit actor and stored creator must be consistent.
- **Member service:** AddMember/RemoveMember must pass the same guard;
  permissions are not wired only in SegmentService. Reads also check
  the corresponding read permission before resource lookup.
- **Denial:** no target/domain lookup revealing existence after
  operation denial, no business write, publish, status change, or success
  audit. Redacted denial telemetry is allowed; do not log JWT/API keys.

HTTP input parsing that does not touch business state can produce
400 before a content-dependent schedule decision. Route-level denial for
base operations need not process JSON. Policy tests use valid paths/
bodies; they do not rely on malformed-input differences to grant permission.

## 5. HTTP/error/compatibility contract

API key and Bearer JWT both remain required. Role permission is not
a replacement for authentication. For M02a, use the existing envelope, without an added
error-code field, ETag, 428, or successful-response changes:

| New condition | Status / message |
| --- | --- |
| Missing/invalid staff identity or inactive | 401; `authentication required` at new permission gate; existing API-key/JWT messages remain safe |
| Active known identity lacks operation/field permission | 403; `insufficient permissions` |
| Current-authority DB/integrity error or unknown internal error | 500; literal `internal server error` |
| Authorized request fails domain validation/state/resource rule | Existing mapped 400/404/409; not changed to permission success |

Example denial: `{"success":false,"message":"insufficient permissions"}`.
No `data`, database role, stack, SQL/DSN, or arbitrary err.Error() in
new denial/500 responses. Wrapped authorization sentinels remain mapped with
errors.Is. List/detail/member handlers must map new errors too; adding
a switch only to Create/Send is insufficient. Fix the three remaining unknown-500
member branches so guard DB failures cannot leak.

This tightens security on existing v1 routes; do not add a toggle
restoring VIEWER mutation access. The frontend must rely on response
status and current permissions, rather than hiding buttons as its only
control. UI and mobile backend are not implemented in this repository. The existing
legacy send 202 is not evidence of a durable outbox; the durability contract changes in M04.

## 6. Delivery slices and verification

1. Shared checker + SQL query/repository + matrix unit tests, including unknown
   operations and shuffled role IDs. Contract tests match the entire role
   set and operation/route map JSON against Go policy; they do not self-assert allow-all.
2. Wire notification/segment/member route and service guards; adapt constructors,
   fixtures, and mocks if interfaces change. Preserve service errors/state
   rules. Document HTTP behavior with clear target/runtime status.
3. Add tagged real-DB/API tests to the dependency harness: all four roles,
   stale claim/live role, direct service calls, scheduling bypass, and no-side-
   effect denials. Do not run a real worker/provider. Check row counts,
   creator/audit IDs, and Kafka absence, not HTTP status alone.

M02a is complete only if the matrix and all relevant acceptance cases pass,
build/default tests/tagged tests/vet/format pass, API docs are updated, constructor
wiring fails closed, and code-review C21 is updated with evidence/remaining
race limitations. A written SPEC is not evidence that everything is already running.

Staff administration service checks, session/JWT revocation, BFF/gateway
perimeter, DTO/redaction, broad body/rate limits, full API OpenAPI contract,
resource-error correctness, and atomic success audit remain subsequent M02 work
or belong to the owning milestone's transactions. USER/mobile authorization, enrollment, and
operational step-up do not automatically gain permission from this role matrix.

## Acceptance cases

| ID | Input / condition | Expected result / side effect | Expected error |
| --- | --- | --- | --- |
| A01 | Each role × each policy permission | Exact allow/deny matrix; unknown/case variant never allowed | Forbidden if denied |
| A02 | Every mapped method/path | Correct route guard; POST/PATCH extra schedule rule applied | Per matrix |
| A03 | Missing/wrong context, typed-nil claims, ID <=0, deleted/inactive actor; direct service invocation too | No domain write/publish/success audit; HTTP 401 | Unauthenticated |
| A04 | Actor authority query fails or role row integrity absent | Fail closed, generic 500, no write; never fake 401/404 | Wrapped internal error |
| A05 | Token says SUPER_ADMIN, DB now VIEWER | Business reads allowed, every one of nine mutation routes denied; DB unchanged | 403 |
| A06 | Token says VIEWER, DB now ADMIN | Feature gates use current DB authority; normal state/validation still required | None from role gate |
| A07 | VIEWER/unknown role calls create/edit/delete/send or membership writes | Uniform operation denial for existing/unknown valid IDs; no domain lookup/publish/write | 403 |
| A08 | OPERATOR creates/edits/deletes draft; manages segment/members | Allowed subject to existing domain guards; real actor attributed | Existing domain errors only |
| A09 | OPERATOR sends, creates with future ScheduledAt or PATCHes non-null ScheduledAt | All denied; no scheduling field/status/target/audit/Kafka change | 403 |
| A10 | ADMIN/SUPER_ADMIN same valid operations | Role gate allows; existing DRAFT/domain rules still enforced | None from role gate |
| A11 | OPERATOR ScheduledAt absent/null on create or title PATCH | No schedule persistence/clear side effect; legacy draft/no-op behavior retained | Existing validation only |
| A12 | Authorized actor supplies CreatedBy of another staff in direct CreateParams | Stored/audit creator is verified actor; no impersonation | None from permission gate |
| A13 | Demotion between route gate and service check | Service independently rechecks committed authority and denies mutation | 403 |
| A14 | Context canceled/deadline during authority lookup | No later write/publish or async permission success | Context/internal error |
| A15 | Wrapped denial/internal error reaches every affected handler | Safe 401/403/500 envelope; no raw details, including member handlers | Mapped error |
| A16 | Known authorized read/write fixtures | Existing success body/status and domain guards preserved; no new owner-only rule | Existing domain errors only |
| A17 | Real DB with shuffled role IDs | Authority determined by joined name/active state, not numeric hierarchy or token role | Per matrix |
| A18 | Existing unit + tagged smoke suites | No skipped assertions/allow-all production fallback; default tests remain credential-free | None |
