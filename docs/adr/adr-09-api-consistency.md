# ADR-09 — API contract, state vocabulary and compatibility

**Status:** proposed, 2026-11-02. Not implemented or accepted. NEW companion
SPEC: `docs/specs/adr-09-api-consistency.md` (not written).
Related gates: [API/data/security readiness](../analysis/api-data-security-readiness.md).

**Scope:** pkg/response, shared request/error helpers, feature handlers/DTOs/
services, SQL state constraints and queries, schema/API documentation and
frontend/mobile/consumer compatibility tests. No frontend UI implementation.


Cross-ADR protocol: [consistency review](../analysis/adr-01-11-consistency-review.md).
Its shared lock/identity rules and verification gates apply to this proposal;
review completion does not mean acceptance or runtime implementation.

## Evidence and dependencies

pkg/response currently emits success/message/data and ignores Encode errors
after WriteHeader. Staff/profile PATCH DTOs use plain strings/bools in places;
pagination helpers silently default invalid input. Notification constants include
SENDING/SENT while SQL/processor use QUEUED/PROCESSING/COMPLETED. Three segment
member 500 branches still expose err.Error(). ADR-01–08 introduce durable send,
modern registration, receipts, event versions, coverage/holds and telemetry;
those proposed contracts must be specified consistently, not mixed with old API
docs as if already implemented. Security/retention are ADR-10/11 dependencies.

## A-1. Preserve the envelope; make errors safe and machine-readable

### Context

Clients need stable outcomes without parsing arbitrary driver/error strings.
An encoding failure must not leave an apparently successful malformed response.

### Decision

Keep success, optional message and optional data as the base envelope. Propose
an additive error object on failures: stable code plus bounded field errors
without echoing secret values. X-Request-ID correlates diagnostics. Clients
branch on HTTP status and code; message remains human-readable. No blanket
change to problem+json or alternate envelopes. Private health/metrics follow
ADR-08's explicitly documented formats rather than pretend to be product data.

Use an allowlisted domain-error mapper with wrapping preserved for logs. 400
for malformed/invalid input, 401 authentication, 403 authorized identity lacks
operation permission, uniform 404 for protected unknown/not-owned objects,
409 state/uniqueness/idempotency conflict, 410 authorized expired receipt,
412 stale HTTP precondition, 413/415 size/media errors, 428 missing required
precondition on NEW conditional writes, 429 throttling and 503 declared delivery
hold/dependency policy. Never collapse a DB outage into 404/empty success.
Unknown 500 uses the exact literal internal server error; never err.Error().
4xx messages also come from safe mapped sentinels, not arbitrary wrapped errors.

Encode bounded typed response DTOs before committing status/headers where
practical. If serialization fails before commit, return safe 500 and log a
sanitized cause; after commit follow ADR-08's abort/outcome policy. No concurrent
response writes. Preserve current successful status/body contracts until an
explicit compatibility change; do not emit a body with 204.

### Alternatives rejected

Human-message parsing and per-handler fallthrough leak internals and diverge.
Replacing every envelope immediately would break callers without improving
the durable send contract.

### Consequences

Shared mapping/parsing helpers are NEW. Fix hold-out leaks during implementation
and update the findings inventory only with verified runtime changes.

## A-2. Canonical states and asynchronous command results

### Context

Go-only constants cannot make invalid SQL status strings safe; admission,
submission and application observation have different completion meanings.

### Decision

Canonical notification vocabulary matches the DB constraint:
DRAFT, SCHEDULED, QUEUED, PROCESSING, COMPLETED, FAILED, CANCELLED.
CANCELLED remains reserved/unimplemented; neither cancel nor FAILED resubmit
is silently added. Delivery vocabulary remains PENDING, SENT, DELIVERED,
OPENED, FAILED with ADR-01/05's guarded early/late observation rules. Retire
unused SENDING/notification-SENT constants and invalid MarkNotificationSent
query only after call-site verification. Persisted reason/action strings are
registered stable constants, not renamed for stylistic cleanup.

POST /notifications/{id}/send returns ADR-01/03's 202 durable acceptance snapshot,
not provider completion. Same-key authorized replay returns the stored result
even during a later dispatch hold; a new request follows coverage/hold policy.
Do not expand idempotency into every endpoint without a defined scope, expiry
and transaction boundary. State validation/CAS occurs in the service/SQL write,
not only by a handler pre-read. A GET is not a send/requeue operation.

Migration HELD/route state and preparation reasons are separate control data,
not additional notification statuses. Mixed completion exposes per-device
outcomes and snapshot units; COMPLETED does not promise every device observed
the push. Partial legacy snapshots/unknown evidence are explicitly unavailable.

### Alternatives rejected

Mapping SENT to COMPLETED everywhere conflates delivery and campaign states.
Interpreting 202 as physically delivered or rewriting old audit constants
breaks integration semantics.

### Consequences

Update Go, SQL, DTO docs/examples and event schemas together. Regenerate sqlc
from SQL; do not hand-edit generated code or change constraint values casually.

## A-3. Field-presence-aware PATCH and concurrency

### Context

Plain bool/string cannot distinguish omitted, false/empty and explicit null.
Read-then-update without version guards can overwrite a newer staff edit.

### Decision

For a modern PATCH contract use application/merge-patch+json with an allowlisted
object schema and presence-aware DTO/patch conversion. Omitted is unchanged,
null clears only nullable fields, false/empty are explicit values subject to
domain validation, arrays replace as a whole. Reject unknown/immutable/security
fields, unsupported top-level shape, trailing JSON documents and ambiguous
duplicate keys; do not apply arbitrary JSON directly to a DB model.

Use server resource versions/strong ETags and If-Match for newly introduced
conditional edits. Missing required If-Match is 428, stale is 412. Installation
registration_version CAS uses ADR-04's documented 409 contract; do not silently
translate it into an ETag error. Existing v1 PATCH/media behavior is not changed
without migration; if clients cannot adopt additive negotiation safely, introduce
a reviewed v2 operation/route rather than silently reinterpret old bodies.

Mutation, returned representation/version and critical audit record commit in
one transaction, using UPDATE RETURNING or a transactionally consistent read.
Do not return a not-found/500 solely because an unnecessary post-commit re-fetch
failed. Unique constraints are authoritative; map constraint violations into
safe domain conflicts. Password/session and ownership changes use their own
security contract, not generic mass assignment.

### Alternatives rejected

Using empty string as omission prevents legitimate clearing. Generic reflective
patching can mutate owner/role/state. A successful stale write is not resolved
by fetching the latest row after the damage.

### Consequences

Typed optional values/shared validators and resource version migrations are
NEW; exact endpoints and compatibility window are specified before coding.

## A-4. Lists, filters and pagination

### Context

Totals and pages need the same visibility/filter predicate; deterministic
ordering does not make offset pages a permanent snapshot during concurrent writes.

### Decision

Retain page/limit for existing lists, defaults page=1/limit=10 and maximum 100.
For the modern validated contract malformed/out-of-range values return 400,
not silent clamping; migration must account for v1 callers relying on defaults.
Check overflow/deep-offset bounds explicitly. Unknown enum/filter/sort is 400;
supported filters compose, including caller visibility and search. Do not fetch
all rows in Go to filter/count. Empty list is [], total=0; beyond-last page
does not fabricate matching results.

List and total use the same SQL predicate and a documented consistent query/
short read snapshot where exact page metadata is promised. Stable tie-breaker
is required (for example created_at plus id). Across separate page requests,
concurrent changes can still move rows; document that limit. New high-churn
delivery/event feeds may use a separately versioned keyset cursor tied to its
filter/order; cursor is not authorization and must not bypass current visibility.

Historical metrics use immutable campaign snapshot/facts from ADR-05, never
current active-user count or a representative device pretending to be all devices.
Default fetch limits in worker internals are not API pagination policies.

### Alternatives rejected

Unfiltered Count and one-filter-priority logic produce misleading pagination.
A million-row fetch/limit is not streaming or proof that an audience is complete.

### Consequences

Batch/count queries and mocks must follow changed interfaces. No new endpoint
may claim exact/complete results while imposing an undocumented result cap.

## A-5. Data representation, mutation/audit and authorization boundaries

### Context

Serialization, sensitive fields and deletion semantics are part of the API
contract, not DTO style preferences.

### Decision

Use UTC RFC3339 timestamps with explicit timezone; absence/null semantics are
documented per field, never year-0001 as a real timestamp. New opaque IDs are
strings; existing v1 numeric IDs are not globally changed without consumer
review. Validate positive decimal path IDs and overflow; do not confuse Laravel
external_id with internal user ID. Confirm client-safe numeric ranges before
retaining an int64 JSON contract indefinitely.

Responses never expose password hashes, address/installation secrets, receipt
capabilities, credential/config blobs or unredacted DB structs. Device DELETE
is documented deactivation/tombstone under ADR-04/10, not cascade history loss.
Keep established success acknowledgements compatible; new operations explicitly
document whether a replay/unknown-owned deletion is a no-op or a protected 404.

Critical staff/role/password/campaign acceptance/migration-control mutations
and a minimized business audit record are one DB transaction. Audit snapshots
use a field allowlist and never include plaintext/hash passwords, JWT or push
addresses. Replay does not create a second send/audit action. Diagnostic logs
and external audit shipping cannot replace that durable record. Existing
best-effort audit behavior is not claimed fixed by this decision.

Authenticate/authorize each service operation including field-level scheduling
and role changes per ADR-11, not just the endpoint prefix. Object non-disclosure
policies apply before idempotency/dedup replay. GET/read APIs remain side-effect
free from notification delivery/operator mutation.

### Alternatives rejected

Stringifying every existing ID or changing every deletion response at once is
not compatibility. Returning internal models and storing full before/after JSON
can expose security material. Handler-only role checks are bypassable internally.

### Consequences

API mapper/DTO tests, transaction tests and audit sanitization tests are required;
new persisted actions are registered in internal/audit/constants.go.

## A-6. Contract ownership and rollout

### Context

Markdown docs, source, planned ADR and generated clients must not become
competing truths with different implementation status.

### Decision

Current behavior is verified from routes/handlers/services plus docs/api. ADRs
describe proposed target behavior. NEW docs/openapi.yaml becomes the reviewed
machine-readable contract when the implementation milestone writes it, covering
implemented and explicitly staged operations with auth/errors/examples. Generate
any needed Postman collection from that contract; do not initialize/publish a
new workspace merely to write this ADR. Keep ownership and synchronization clear.

Inventory existing frontend/mobile/integration callers before breaking auth,
media types, error/status, ID, PATCH, statistics or event semantics. Prefer
additive changes with documented rollout/deprecation; use a versioned path when
semantics cannot coexist safely. Legacy auth/enrollment cannot remain an
unguarded bypass during the transition. Acceptance tests cover real compatibility,
not only whether OpenAPI parses.

### Alternatives rejected

Calling a future ADR behavior implemented in docs/API examples misleads clients.
Parallel hand-maintained specs/collections without ownership drift quickly.

### Consequences

Before SPEC acceptance decide operation/version migration map, legacy consumer
inventory, exact safe error codes, PATCH/version scope and numeric SLO. All
runtime changes/tests remain future work; no API doc is rewritten as live here.

## Sources and design verification

Primary references checked 2026-11-02:
[HTTP semantics](https://httpwg.org/specs/rfc9110.html),
[JSON Merge Patch](https://www.rfc-editor.org/rfc/rfc7396).
No endpoint invocation, API artifact generation or publication was performed.

| Case | Expected result in companion SPEC |
| --- | --- |
| Wrapped DB failure on lookup/list | Generic 500, never false 404/empty success |
| PATCH omitted/null/false/empty | Distinct, schema-validated effects; forbidden fields rejected |
| Stale/missing required conditional version | 412/428, no mutation/audit side effect |
| Registration CAS stale | Documented 409 preserved |
| Concurrent uniqueness conflict | One winner, safe domain 409, no leaked SQL |
| Filtered/multi-filter list or beyond-last page | Composed predicates, correct total/visibility, stable order |
| Encoding/panic before/after headers | Safe response or explicit abort; no fake successful wire result |
| Send replay during hold | Authorized committed 202 snapshot, no second action |
| Audit insert fails in critical write | Whole transaction rolls back; secret-free evidence |
| Legacy caller | Explicit compatibility/version test, no silent contract reinterpretation |
'''
