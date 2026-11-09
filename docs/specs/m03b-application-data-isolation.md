# M03b — Existing data, queries and API application isolation

2026-11-03. **M03b.1 inventory/design complete; M03b.2a additive schema/tooling locally verified.**
Business query/API/worker isolation is not implemented.
[Expansion contract](m03b2-business-scope-expansion.md),
[verification](../analysis/m03b2a-verification.md).
[M03a registry](m03a-application-registry.md) is locally implemented; every project
remains PROVISIONING. Normative boundary: [ADR-12](../adr/adr-12-multi-application-isolation.md).
Source baseline: [reviewed inventory](../contracts/application-scope-inventory.json)
and [inspection evidence](../analysis/m03b1-scope-inventory.md).

## 1. Completion boundary and chosen transition

M03b must give every existing business row an immutable application owner,
qualify all reachable reads/writes/joins/counts with that owner, and remove every
unscoped business entry point before introducing app-B business data. Adding a
nullable application_id, a route prefix or a grant check alone does not qualify.
Staff directory/roles/auth/profile stay global. Registry management stays global;
registry membership reads keep M03a's explicit grant rules.

Choose **retirement of the 24 existing global business HTTP operations** at
cutover, rather than building an optional single-app compatibility bridge. No
client compatibility requirement for those paths has been established. This is
a planned breaking change, not permission to silently remove deployed routes
now. Record affected consumers and coordinate their replacement/release before
cutover. If a real compatibility need emerges, write a separate exact bridge
SPEC with approved server-bound callers/app; never infer scope from a first app,
external_id, token lookup, request header or global default. No bridge is built
as speculative infrastructure.

Old worker/scheduler processes must be replaced with verified scoped processes
or drained/stopped and prevented from restarting. A held app B is insufficient:
app A's old BROADCAST query still selects B users. HTTP retirement alone also
cannot stop that path. M03b may finish with old dispatch stopped; operational
activation still waits for M04–M06 and per-app identity/provider gates. Metadata
can remain PROVISIONING throughout. No ACTIVE status migration is included.

## 2. Reviewed inventory

The machine-readable inventory enumerates all 16 current tables, 135 named SQL
queries across 15 files, 46 feature HTTP operations and four binaries. Of these,
11 tables, 100 named queries and 24 HTTP operations concern legacy app-owned
business data. Names include currently unexposed repository functions: absence
of a route is not evidence a global query is safe to keep in a new caller.
Audit's four queries are mixed-scope and need separate scoped projections.
The 20 role/staff queries and 11 registry queries remain deliberate control-plane
operations, not blanket exemptions for business joins.

| Aggregate / source query file | Required application scope and references |
| --- | --- |
| users.sql | Owner plus app-qualified external subject resolution, search, status, count and all projections |
| device_tokens.sql | Owner and same-app user; token lookup/update/delete/status/bulk audience lookup; no raw-token staff DTO |
| segments.sql | Owner; exact-name conflict and list/search/count checks within app; creator remains global staff |
| segment_members.sql | Owner and same-app segment/user; membership existence, insert/delete, user projection and batched counts |
| templates.sql | Owner; same-app template lookup/name/status, including currently repository-only operations |
| notifications.sql | Owner; same-app optional template, draft CAS, all list/filter/stats subqueries and due scheduling |
| notification_targets.sql | Owner and same-app notification plus every nonnull segment/user/upload reference |
| notification_deliveries.sql | Owner and same-app campaign/user/device, with device belonging to that user; history/count/stat projections |
| notification_reads.sql | Owner and same-app campaign/user; scoped existence, uniqueness and reader list |
| upload_batches.sql / upload_batch_rows.sql | Owner and same-app parent batch; quarantine/retain historical data; no new UPLOAD feature |
| audit_logs.sql | Preserve global staff audit; new business audit must have resolved owner; scoped search/detail/count must exclude other/global apps |

Application table ownership is enforced by FK to applications. Child tables
carry application_id explicitly and reference parent (application_id,id) pairs.
Every parent needs an appropriate unique key for the composite references.
Deliveries additionally require a device/user ownership constraint, not merely
three separately app-valid IDs. Every nonnull target reference must be checked,
even legacy rows with extra target fields allowed by today's weak CHECK.

Do not give application_id a database default. New scoped writes always supply
an internal ID obtained from resolved authority, never a client body/query value.
Final business columns must be NOT NULL, have validated FKs/checks and immutable
ownership enforcement. Prohibit owner reassignment even if the new app's parents
would satisfy all FKs; test direct SQL UPDATE as well as API attempts. Aggregate
moves/cross-app linking are excluded. Define the concrete enforcement function/
trigger and deployment privileges in the schema slice before generating sqlc.

## 3. Migration protocol (offline ownership transition)

Use an explicit quiesced transition; no online dual-write/backfill protocol is
claimed. Exact migration SQL and executable preflight/backfill tools belong to
M03b.2, before changing generated code.

1. Inventory the target deployment without reading credentials into logs. Record
   backup/restore evidence, current binary versions, consumer offsets/pending
   work, affected clients and approved application public UUID. Bootstrap never
   creates or chooses a default app. Production data ownership is not yet known.
2. Quiesce **all** business API writes, mobile sync/token management, schedulers,
   workers, replay/maintenance writers and direct SQL jobs. Drain provider calls
   and reconcile uncertain work under its owning delivery milestone; do not
   equate an empty HTTP queue or committed Kafka offset with no pending work.
   Record who prevents old binaries from restarting. Registry/staff access must
   not race mapping/constraint changes; use a maintenance window for the transition.
3. Expand schema with nullable application columns if needed for staged rollout.
   Retain today's uniqueness while legacy queries exist. No default, implicit
   project, new dispatch eligibility or app-B rows during this intermediate state.
   Expansion alone does not make an old binary safe for multi-app data.
4. Preflight ownership and referential consistency, then apply an operator-reviewed
   mapping to an explicitly selected existing PROVISIONING project. A one-app
   mapping is permitted only with evidence that all included business data belongs
   to it. Registry creation date/order is not evidence. No user/device ownership
   is derived from email, external_id, token string or JWT claims.
5. Derive child ownership from mapped parents and verify *all* parent edges agree.
   Count/report unresolved roots, orphans, cross-parent conflicts and inconsistent
   device/user links. Any unresolved record blocks finalization. Do not invent an
   owner, delete history or silently drop rows. Quarantine requires an explicit
   reviewed durable archive/preservation procedure before removal from live tables;
   no quarantine procedure is implemented in this slice.
6. Map historical business audit by explicit entity classification and resolved
   owner; do not infer from entity_id alone because IDs overlap across tables.
   Deleted/unknown-entity audits may have no resolvable owner: retain restricted
   legacy evidence and exclude it from app-facing reads, with explicit unresolved
   counts and release disposition. Global staff/profile audit stays global. No
   hash/credential snapshots are copied; M02c cleanup remains its own gate.
7. Validate composite constraints and owner immutability; finalize NOT NULL and
   scoped uniqueness only after the new query/services contract supports it.
   Compare per-table counts and key/reference consistency before and after; use
   A/B fixture tests and a restored copy to validate the actual procedure.
8. Deploy only the verified scoped API/query binaries, with all old business HTTP
   registrations retired and old background processes disabled. Verify startup/
   deployment controls, not merely source removal. Reconcile or durably hold old
   queued events: never relabel their ownership on replay. Keep all projects
   nonoperational until their separate activation gates pass.

M03b.2's first additive migration may safely leave existing rows unassigned, with
legacy-only development behavior still present. It MUST be labelled incomplete
and must not relax global identity/address uniqueness or enable multi-app data.
The final cutover is a separate gate. A down migration is not a production rollback
plan: after app-B data exists, rolling back to global readers/writers is forbidden.
Restore requires compatible binaries, ownership/epoch evidence and sealed dispatch;
M09 owns coordinated production recovery.

### Keys, identity and address caveats

Scoped external subject uniqueness ultimately includes app and trusted identity
namespace (M03c). Legacy external_id is only a migration input, not a universal
subject or authentication policy. Do not silently copy legacy JWT claims into a
new trusted namespace. Segment name uniqueness must be enforced atomically per
app after an explicit duplicate/case-policy preflight; do not add an unreviewed
case-folding rule. Exact key/index definitions belong to M03b.2.

Do **not** replace device_tokens' global push_token unique index with
(application_id,push_token) and call address ownership solved. Retain the existing
conservative constraint during migration. M03c specifies trusted provider-project/
address namespace, protected fingerprints, active-claim uniqueness and immutable
binding history before relaxing it. No automatic OneSignal ID -> FCM token
conversion or registration eligibility; legacy address provenance is unverified.

Composite FK replacement must preserve or explicitly review deletion behavior.
In particular a template's existing SET NULL must never null application_id,
while references to a device must also preserve the user match. Test cascade,
optional-reference nulling and immutable history against the actual migration;
do not rely on composite FK defaults. New hard-delete APIs and retention policy
are not introduced here. Deferred UPLOAD rows/references still need isolation;
no UPLOAD acceptance or dispatch path is added.

## 4. Query, authorization and API contracts

Modern business routes use explicit `/api/v1/apps/{app_id}/...` context. This is
a namespace decision, not a claim those routes now exist. Write each aggregate's
exact OpenAPI/DTO/permission/conditional semantics before implementing its routes;
do not copy all legacy DTOs or manufacture endpoints for repository-only tables.
Raw push tokens, secrets and installation proofs must never enter staff list,
audit or error DTOs. Generic mobile enrollment remains M03c/d, not a path rename
of today's Laravel-dependent sync.

Require current authenticated principal + active explicit app grant + allowed
operation at both route and service. Use the existing app authorizer: unknown/
ungranted apps 404, granted but disallowed operation 403, PROVISIONING business
operation 409, DB failures safe500. SUPER_ADMIN has no business wildcard. An
operation-specific detail/list/count must not resolve a foreign row globally
before testing scope. After app authority, foreign/missing resource IDs return
identical 404 without echoed references; infrastructure errors must not masquerade
as not-found. New users/device-metadata permissions need explicit matrix entries;
legacy staff JWT alone is not their future role policy.

Repositories take a mandatory validated app ID in every business method. No
optional pointer, zero-as-global convention, context-only hidden filter or fallback
to old methods. Split deliberate global control-plane repository methods from
business methods. Scope primary lookups, writes, CAS predicates, existence checks,
search/count, bulk ANY(ID[]) calls, UNION/subqueries, joins and pagination alike.
Mutation rows-affected and ownership checks must refer to the same app-qualified
resource. Avoid TOCTOU membership/reference checks without transaction constraints.
Use one eligible SQL snapshot for count/page where that is the documented promise.

For mutating operations use transaction-bound queries and atomic scoped audit;
acquire app/control locks before resource/child locks in a documented order. Check
current DB authority inside the transaction. Staff business writes lock the
session lineage and project before the actor and session, then check the current
project grant. Recheck session and access-token expiry before committing the
mutation and audit. Preserve DRAFT-only campaign edit/
delete and role send/schedule rules. Exact CAS/retry/claim semantics belong to
aggregate SPECs and M04/M05; do not port unsafe best-effort multi-write compensation
as the new contract. Regenerate mockery when changing mocked interfaces.

Read-only fixtures may use isolated SQL/repository/service tests while projects
are PROVISIONING; a real HTTP business success cannot bypass the status gate.
Do not add an allow-all authorizer or ACTIVE enum merely to make tests pass.

## 5. Background and event boundary

Inventory every call to global audience, token expansion, due-list, state update,
delivery/stat/history and replay methods, including repository-only functions.
Old send events identify only notification_id; old delivery updates carry user/
campaign IDs without app or version. Adding an app field does not update old
consumers. Never trust copied event scope without resolving and matching persisted
ownership. Missing/mismatched scope must be durably held/rejected under M05's
source-aware event protocol; offset safety cannot depend on logging and skipping.

Until scoped background admission/execution is implemented, old worker/scheduler
must be stopped before A/B business fixtures are connected to an executing stack.
Preserve existing notification_<id>/delivery_<id> key bytes; partition mapping and
versioned event changes require their own reviewed transition. Do not change Kafka
keys or wire FCM as part of a schema-only isolation slice. M04 owns durable
acceptance/scheduler handoff, M05 scoped admission/snapshot/DLQ, M06 provider route/
execution and updates. These are separate from M03b's structural isolation gate.

## 6. Implementation slices and acceptance

| Slice | Concrete output | Completion evidence |
| --- | --- | --- |
| M03b.1 (this checkpoint) | Reviewed source inventory, checker, ownership/backfill/retirement SPEC | Source coverage matches; drift detected; no runtime isolation claim |
| M03b.2 | Exact schema/preflight/backfill SPEC, paired migrations, generated sqlc and migration tooling | Clean and populated fixture upgrade/rollback, unresolved ownership rejection, validated composite/immutable constraints; production procedure still gated |
| M03b.3 | Scoped user/device metadata and segment/member repositories/services/contracts | Same subject/labels across apps where policy permits; cross-app lookup/join/count/mutation denied; no token leaks; revocation/DB failures |
| M03b.4 | Scoped campaign/template/target/delivery/read/upload/audit access and projections | Cross-app references rejected atomically; safe optional/delete behavior; draft invariants; full no-side-effect negatives; no deferred features added |
| M03b.5 | Legacy retirement/deployment gate and complete isolation verification | Every inventory entry scoped, intentionally global or unreachable; old writers stopped; A/B API/SQL/background tests and rollout/rollback evidence |

M03b.2a expansion and scoped API slices M03b.3–4 are locally verified.
[M03b.5 repository retirement and ownership finalizer](m03b-cutover.md)
prepare the production transition. Deployment evidence and identity/address
constraints remain open. Each may need focused commits; these labels are
work groups, not time estimates or a fixed count of coding tasks. Current source
inventory checking is not the final runtime isolation checker. Activation can
remain blocked on M03c/d and M04–M06 even after structural M03b checks pass.

Required negatives: app A principal + app B ID in detail/patch/delete/member/target;
foreign joined rows/counts/search/history; revoked grant/current demotion; raw DB
failure; attempted owner change; conflicting parent references; copied/missing
event scope; stale CAS; audit failure rollback; all old global entry points. Failed
mutations must leave business/audit/outbox/Kafka/provider observations unchanged.
No credentials, live project identifiers or actual mobile/provider access are
needed to establish these local structural invariants. Production mapping,
consumer cutover, identity/provider origin evidence and operational gates remain
explicit and cannot be satisfied by fixtures.
