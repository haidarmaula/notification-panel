# M03b.1 — Source inventory and isolation transition

2026-11-03. **Inventory/design checkpoint only; business isolation is not yet
implemented.** Parent [SPEC](../specs/m03b-application-data-isolation.md),
[machine inventory](../contracts/application-scope-inventory.json).
M03a's registry remains locally verified and PROVISIONING-only.

## Findings and resulting work

| Source | Inspected behavior | Required consequence |
| --- | --- | --- |
| db/migrations/000001–000014 | Eleven business tables lack application_id; external_id/push_token uniqueness is global and child FKs use IDs alone | Explicit root mapping, composite references, immutable owner, scoped queries; no automatic app/default mapping |
| db/queries/*.sql | 135 named queries: 100 legacy business, 20 roles/staff, 11 registry, four mixed audit | Inventory every query, including repository-only uploads/templates/reads, bulk filters and counts; split scoped audit access from global control |
| internal/features/users/routes.go + cmd/server/main.go | Six staff read operations use API key + staff JWT; three device writes use global mobile identity middleware; no app selector/grant | Replace staff business reads with scoped permission+grant checks; mobile writes need M03c/d identity contract, not a route-only rename |
| internal/features/mobile/{module,routes,service}.go | Server mounts POST /mobile/sync with no outer middleware; service checks the body JWT, resolves external_id globally and looks up/registers tokens globally | Retire app-less sync at cutover; no generic per-app identity/proof can be inferred from that path |
| internal/features/segments/{routes,service,members_service}.go | Eight role-protected operations have global segment/user/member repositories | Keep operation guards and add explicit app authority, scoped member/parent constraints, atomic mutations/audit |
| internal/features/notifications/{routes,service}.go | Six role-protected operations; Send uses DRAFT→QUEUED CAS and direct Kafka publish with best-effort compensation | Preserve current CAS behavior while scoping; durable acceptance/audit/outbox remains M04, not already solved by scope |
| internal/features/notifications/processor.go: expandTargets | BROADCAST calls ListByStatus(ACTIVE); SEGMENT reads global membership; token expansion uses ListByUserIDs without app | App B hold cannot protect its users from app A old worker; scope or stop every worker before app-B business rows |
| cmd/scheduler/main.go: processScheduled | Lists all due notifications, updates by ID and directly publishes old event | Scope or stop scheduling; durable handoff and expired/uncertain work remain M04 |
| internal/kafka/events.go | Send event links by campaign ID only; delivery update lacks app/version | Persisted ownership must be verified; versioned source-event/offset handling belongs to M05/M06; old consumers must not remain active |
| db/queries/{notifications,notification_deliveries,notification_reads,audit_logs}.sql | Global statistics/history/count/audit projections; joined rows can expose data even when a main row is guarded | Scope every join/subquery/count and separate restricted unresolved historical audit from app reads |
| cmd/worker/main.go | Wires global repositories and OneSignal provider explicitly | This checkpoint neither swaps FCM nor establishes provider/app routing |

The counts describe source declarations, not runtime traffic, active production
consumers or an observed production dataset. No credential, Firebase/APNs setting,
mobile source, real data or deployed server was read.

## Route disposition

The inventory enumerates 46 HTTP operations:

- **24 global business operations to retire at cutover:** six user reads, three
  device writes, one mobile sync, eight segment/member operations, six campaign
  operations. Removal is planned; these routes still work as before today.
- **12 global control-plane operations to retain:** two auth, three profile,
  seven staff administration.
- **10 provisioning registry operations to retain:** caller-granted metadata
  plus global SUPER_ADMIN registry/grant administration.

Retirement is the M03b baseline because no legacy HTTP compatibility requirement
has been established. It requires consumer/cutover review before deployment.
A compatibility bridge is not implemented. The 46 production checklist step
numbers coincidentally match this route count; they are unrelated measures.

## Migration decisions

Use a quiesced ownership transition and reviewed explicit application UUID;
never seed/default ownership automatically. Stage expansion without enabling app
B, then preflight all parent edges, map verified roots/children, validate composite
constraints and owner immutability, finalize scoped uniqueness/NOT NULL and deploy
only the scoped/retired paths. Keep global token uniqueness until M03c defines
trusted provider address conflicts. Preserve all unresolved evidence or block;
no fabricated owner or silent purge. Deleted/ambiguous historical business audit
requires a restricted legacy disposition, not exposure through app A/B reads.

M03b.2 must specify exact SQL/preflight/backfill tooling and test clean/populated
migrations before runtime/schema changes. Production ownership and consumers are
unknown; that blocks actual cutover, not continued local implementation. Scoped
business API success/activation remains gated while projects are PROVISIONING.

M03b is divided into five work groups: this inventory/SPEC, schema/migration,
scoped users/devices/segments, remaining business aggregates/projections, and the
complete legacy retirement/isolation gate. M03b.2–5 remain unimplemented. These
are not fixed-duration tasks. M03c/d and durable dispatch milestones still follow.

## Checks and limits

`python3 scripts/check-application-scope-inventory.py` matches the checked-in
manifest against current table declarations, named SQL queries, literal/const-
prefix feature registrations and cmd entry points. Unsupported encountered route
expressions fail closed. This is a development inventory drift check, not a Go
call-graph/security analyzer, live deployment check, SQL semantic validator or
proof every future registration style is detected. Scope classifications and
route dispositions still require human review.

Validation covers the positive inventory, temporary manifest drift in each of
four categories (table/query/route/binary), invalid/duplicate entries, script
syntax, local Markdown links and diff whitespace. No Go/SQL runtime file changed;
Go, Docker integration and provider tests were not repeated. M03a.3's checks
remain historical evidence for that registry slice only. No A/B business data,
new endpoint, migration or production isolation test was introduced here.
