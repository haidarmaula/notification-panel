# M03a — Application registry and staff scope foundation

2026-11-03. **M03a.1–3 registry persistence, resolver/services and HTTP locally implemented/verified.** [Persistence](../analysis/m03a1-verification.md),
[service evidence](../analysis/m03a2-verification.md),
[HTTP evidence](../analysis/m03a3-verification.md).
Requirement: one admin panel can manage multiple independent application projects.
[ADR-12](../adr/adr-12-multi-application-isolation.md) defines the boundary;
[this OpenAPI](../contracts/application-registry.openapi.json) defines the implemented registry HTTP
shapes. Existing routes/permissions do not change merely because this SPEC exists.

## 1. Scope and permissions

Add a backend `applications` feature and registry metadata, explicit current-DB
staff grants, an app-scope resolver and atomic control-plane audit. No business
data backfill, enrollment, provider loading, activation, dispatch or mobile change.
UI calls an application a project; internal application and public project are
one identity, not two nested entities. Android+iOS can belong to one project.

Add `applications.read` for the four known roles, and `applications.manage` only
for SUPER_ADMIN to the server-owned permission map and its canonical matrix/tests.
Both route and public service independently recheck current DB authority; no JWT
role/app claim or allow-all fallback. Global staff auth/profile/directory stay separate.

- `/api/v1/admin/apps...`: global management permission. SUPER_ADMIN can inspect
  registry/grants and administer any project, including ones without a business grant.
- `/api/v1/apps...`: current known role plus active app grant, including SUPER_ADMIN.
  List returns only granted projects; detail of missing/ungranted app is uniform 404.
- Creating a project does NOT silently grant business access. SUPER_ADMIN explicitly
  grants themselves or another active staff account through the audited grant API.
- App grant only narrows access; existing role still controls read/draft/send/schedule.
  It never promotes VIEWER/OPERATOR or replaces current DB role checks.

This concretizes ADR-12's proposed grant policy for implementation. G5 production
policy review remains open; no new project/business routes are enabled by this doc.

## 2. Candidate schema and migration

Migration 000016 is now implemented and fixture-verified for M03a.1.
Create paired up/down files BEFORE sqlc generation; no generated files hand-edited.

| Table/column | Definition / invariant |
| --- | --- |
| applications.id | BIGSERIAL PK, CHECK >0, internal only; no public ID reuse |
| applications.public_id | UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(); opaque API identity |
| applications.key | VARCHAR(63) NOT NULL UNIQUE, immutable; regex `^[a-z][a-z0-9-]{1,61}[a-z0-9]$` |
| applications.name | TEXT NOT NULL, trimmed nonempty, at most 128 UTF-8 bytes; byte length enforced in DB and API |
| applications.status | TEXT NOT NULL DEFAULT PROVISIONING; CHECK permits ONLY PROVISIONING in M03a |
| applications.revision | BIGINT NOT NULL DEFAULT 1, CHECK >0; increment on metadata change only |
| applications.created_at/updated_at | TIMESTAMPTZ NOT NULL DEFAULT current_timestamp |
| application_staff_grants.application_id | BIGINT NOT NULL FK applications(id) ON DELETE RESTRICT |
| application_staff_grants.staff_id | BIGINT NOT NULL CHECK >0 FK staff_users(id) ON DELETE CASCADE |
| application_staff_grants.is_active | BOOLEAN NOT NULL, default true; revoke softly, retain row/version |
| application_staff_grants.revision | BIGINT NOT NULL DEFAULT 1, CHECK >0; increment on real active-state change |
| application_staff_grants.created_at/updated_at | TIMESTAMPTZ NOT NULL DEFAULT current_timestamp |
| application_staff_grants PK | (application_id, staff_id); index (staff_id, application_id) WHERE is_active |
| audit_logs.application_id | New nullable BIGINT FK applications(id) ON DELETE RESTRICT, indexed with created_at/id |

No private keys, Firebase JSON, unvalidated policy refs, app secrets or credentials
are accepted/stored in these rows. Provider/auth policy binding is a later scoped
SPEC when its referenced policy model exists; M03a cannot masquerade as configured.
Global audit actions retain NULL application_id. New application/application_grant
audit entities require application_id (DB CHECK); existing legacy entity payloads
are unchanged. Add the nullable field to audit queries/repository params through
sqlc; existing audit call sites keep NULL unless explicitly scoped in later work.

Application rows have no created_by FK: creator attribution is the mandatory
transactional audit. This avoids a staff FK lock during initial app insertion
before the owning transaction reaches principal/audit work. No application hard
DELETE or key/public-ID update endpoint/query. Down migration is for fixtures/
unreleased rollback: drop new audit FK/index/column, grant table, registry in
that order. A production downgrade would destroy registry/access/audit context;
require an explicit coordinated rollback plan, never automatic rollback on startup.

Staff deletion cascades only current grant records, so it does not introduce a
new FK blocker into the existing staff Delete API. Prior grant audit facts keep
the target ID as metadata and remain. Staff deletion itself still has its existing
staff-audit FK/best-effort limitations; no claim of fixing that transaction here.

## 3. HTTP surface

All routes: existing API key AND HS256 staff access JWT/active account, current DB
role guard, independent service guard. New write DTOs reject unknown/duplicate
fields (including IDs, status, credentials), malformed/multiple JSON and bodies
above 64KiB. Existing 16 JSON handlers retain their M02d compatibility behavior.
Name validation rejects controls before trimming; permitted surrounding Unicode
whitespace is normalized, then the nonempty/128-byte budget is checked.
New JSON writes require application/json (optional charset=utf-8); missing/other
media types or non-identity Content-Encoding return 415. Validate raw UTF-8
before JSON conversion and reject control characters in the decoded name
(including NUL/newlines); do not let PostgreSQL turn bad text into a 500.
DELETE accepts empty body only (bounded read; oversized
413, nonempty 400). Do not apply this stricter policy globally to legacy clients.

| Method/path | Purpose | Normal success |
| --- | --- | --- |
| GET /api/v1/apps | Caller-granted project metadata, any known role | 200 |
| GET /api/v1/apps/{app_id} | Caller-granted project metadata | 200 + ETag |
| GET /api/v1/admin/apps | Global registry, SUPER_ADMIN | 200 |
| POST /api/v1/admin/apps | Create PROVISIONING project from key/name | 201 + Location + ETag |
| GET /api/v1/admin/apps/{app_id} | Global project detail, SUPER_ADMIN | 200 + ETag |
| PATCH /api/v1/admin/apps/{app_id} | Change name only, SUPER_ADMIN | 200 + ETag |
| GET /api/v1/admin/apps/{app_id}/staff-grants | Grants including revoked, SUPER_ADMIN | 200 |
| GET /api/v1/admin/apps/{app_id}/staff-grants/{staff_id} | Grant detail, SUPER_ADMIN | 200 + ETag |
| PUT /api/v1/admin/apps/{app_id}/staff-grants/{staff_id} | Ensure active grant with `{"is_active":true}`, SUPER_ADMIN | 201 first row, otherwise 200 + ETag |
| DELETE /api/v1/admin/apps/{app_id}/staff-grants/{staff_id} | Soft revoke, SUPER_ADMIN | 204, no JSON body |

Project DTO: UUID id, immutable key, name, status PROVISIONING, decimal-string
revision, created_at, updated_at. Grant DTO: project UUID application_id,
positive decimal-string staff_id, is_active, decimal-string revision, timestamps.
No internal app row ID, password/hash, token, secret reference or provider config.
Lists use data `{items:[],total:0,page:1,limit:20}` under the existing envelope;
page 1..1000, limit 1..100, strict 400 outside range. Stable order created_at/id
ascending. Admin registry optionally filters by exact `key`; other new query
parameters reject with 400. Grant list includes active and revoked, no implicit
filter. One SQL statement must derive filtered count/page from the same eligible
set/snapshot, including empty/out-of-range pages. No cross-request snapshot promise.

Malformed UUID/staff ID =>400; missing/deleted/inactive identity =>401;
management role denial =>403; unknown/ungranted app or absent grant =>404;
duplicate immutable project key or inactive grant target =>409;
missing write precondition =>428; stale precondition =>412; oversized body =>413;
unsupported JSON media type =>415; unknown failures =>500 literal internal server error.
Errors use the existing envelope, never request contents/DB errors. Authentication
and management permission precede path/body validation. App detail resolves
membership before returning metadata, with no existence oracle for nonmembers.

## 4. Concurrency and retries

All responses use Cache-Control: no-store. Strong ETag wire forms are
`"app:<public_uuid>:<revision>"` and `"grant:<public_uuid>:<staff_id>:<revision>"`,
using canonical lowercase UUID/base-10 IDs. Clients treat them as opaque and
copy them unchanged; they are concurrency validators, not authentication. PATCH
requires one exact If-Match from a current project detail response. PUT first
grant requires If-None-Match: *; existing/reactivated grant requires exact If-Match
from grant detail. DELETE requires exact grant If-Match. Both headers together,
weak tags, lists and unsupported wildcards are 400; missing precondition is 428.
Do not expose a client-chosen numeric version as trusted current authority.
Validation order: route authentication/permission, IDs, media/body rules and
precondition syntax/presence, then transaction authority/resource lookup and
exact current ETag comparison before state changes. A syntactically valid strong
ETag for another resource fails 412; it is never identity proof.

Check precondition even for no-op state. Matching no-op rename/active grant/revoke
returns its normal success without revision/timestamp change or extra audit.
Real changes increment only that resource's revision (grant changes do not alter
project revision). Soft revocation keeps the revision monotonic across regrant,
preventing an old ETag from authorizing an ABA regrant/revoke. Concurrent requests
with the same revision cannot both change state. Duplicate key and membership
races are decided by DB constraints/locked state, not advisory existence checks.

POST rejects If-Match/If-None-Match headers with 400 rather than silently ignoring
an unsupported write condition. POST retries after uncertain acknowledgement reconcile by immutable key through
admin list filter. Same key is 409, not an invented idempotent replay. PUT retries
with a stale creation/update precondition can be 412; GET the grant to reconcile
state rather than blindly changing a new membership. Business idempotency/outbox
acceptance remains M04; no Kafka event is emitted by this registry API.

Every actual registry/grant mutation AND its success audit commit in one DB
transaction; audit insert failure rolls back metadata/grant changes. Precheck
permission before target I/O; inside transaction recheck using transaction-bound
queries. Construct both repositories/authorizer and audit writer from the same
transaction-bound query set; never call a pool-bound audit service from inside
the transaction. Lock the staff session lineage, then the application for
existing resources, before locking staff and session rows. Grant mutations
lock the actor and target in ascending staff ID order, then check current
authority and target state before changing the grant. New application
creation checks and locks the actor and session before inserting the row.
Audit actor comes from checked identity.
Use READ COMMITTED and bounded whole-transaction retries for 40001/40P01 (at most
3 attempts, context-aware); never retry only the audit/write after a partial commit.
Registry and project business write transactions share one five-second operation
budget across pool acquisition, resource locks, callback queries, audit, commit
and retries. An earlier caller deadline wins. Every transaction callback receives
the bounded context. Rollback uses a separate one-second cleanup budget that
survives request cancellation. Timeout failures retain the generic 500 mapping
and leave uncommitted resource and audit changes rolled back.
No external effects occur inside the transaction. Response loss after commit is
still possible and uses the reconciliation rules above.

The transaction holds the actor, session and lineage locks through commit.
It checks session and access-token expiry again before commit. An expired or
revoked session returns 401 and the transaction rolls back. The application
lock also serializes grant writers and protected project lifecycle changes.

For application audit entities use entity_id = internal application ID; for
application_grant use entity_id = target staff ID, disambiguated by the mandatory
application_id. Do not add a historical FK to the target staff row; deletion must
not erase grant audit facts. EntityName is the sanitized current project name.
Register APPLICATION_CREATE, APPLICATION_UPDATE, APPLICATION_GRANT_ENABLE,
APPLICATION_GRANT_REVOKE and application/application_grant entity constants in
internal/audit/constants.go during implementation. New action names do not rename
existing persisted values. Use allowlisted metadata/grant snapshots; never raw
staff rows. Create grant audit: Before NULL; reactivation/revoke: safe before/after;
no-op: no success event. Always set the new application_id on these new events.

## 5. Scope resolver and activation boundary

A required, no-fallback app resolver accepts UUID plus operation. In one authority
statement join staff/current role to optional app and its grant so a missing app
does not erase the authenticated staff result: missing/inactive staff =>401,
corrupt role lookup =>internal500, unknown role/permission =>deny, missing app or absent/revoked
grant =>uniform404, insufficient permitted operation =>403. Return a minimal
principal + internal app identity/status, never staff hash/profile.

PROVISIONING with an active grant permits applications.read metadata only.
Business permissions on a granted project return 409 application is not active;
no scoped campaign/device API is wired in M03a. All projects are nonoperational
at the DB CHECK and API layer. Resolve/check afresh per service call; cached route
checks do not authorize service calls. Exact status/permission precedence must
avoid exposing app state to ungranted users. No implicit SUPER_ADMIN wildcard.

Keep existing global business routes/worker/scheduler unchanged in this slice;
DO NOT insert any second-app user/device/campaign or backfill legacy rows. No
old route gains application isolation from this resolver. M03b must scope or
retire those readers/writers and background jobs before new app data activation;
application metadata/grant fixtures alone cannot establish that gate. Expose no
activate/send/enroll, cross-app broadcast or credential upload endpoint here.

## 6. Required acceptance before completion

1. Migration up/down/up on fixture; SQL constraints/defaults/uniqueness/FKs and
   no operational status; generated sqlc retained, no manual edits.
2. Four-role matrix on all ten routes plus direct-service denials, stale JWT role,
   inactive/deleted actor, no-grant SUPER_ADMIN business detail, explicit self-grant.
3. Two registry fixtures A/B: scoped list/count/detail, revoked/absent grant,
   forged body/path scope, guessed UUID/IDs, safe error precedence and no default app.
4. Strict new JSON/media/body behavior, duplicate/unknown fields, UUID/staff ID,
   byte/name/key bounds, pagination and empty/out-of-range count consistency.
5. Duplicate-key create, matching no-op, stale/missing ETags, concurrent CAS,
   grant first-create race, revoke/regrant ABA and target staff deletion/inactivity.
6. Real-DB atomic audit rollback (inject insert failure), safe metadata snapshots,
   audit app FK/entity checks, actor attribution and old global audit NULL compatibility.
7. Required checker injection/current authority reload, no raw credentials in DTO/
   audit/error/log; provider/auth config absent; no Kafka/provider call on any route.
8. Existing M02/staff delete/audit/body/JWT tests remain green. Record exact checks
   and limits; tests have NOT been run for this proposed feature yet.

## 7. Implementation slices

M03a.1 paired schema/query/sqlc and repository constraints; M03a.2 resolver/services
and atomic audit/CAS; M03a.3 ten routes/strict decoder, API docs and real-DB tests.
Complete these before claiming M03a done. Provider/auth policy registry and fully
scoped business data stay with their owning M03b/c/M06 SPECs. This document does
not settle G3/G5/G7 release acceptance or authorize deployment/live project setup.
