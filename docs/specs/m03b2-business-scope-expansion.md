# M03b.2a — Additive ownership expansion and reviewed backfill

2026-11-03. First runtime/schema slice of [M03b](m03b-application-data-isolation.md).
Migration **000017** expands ownership only; final NOT NULL/scoped identity keys,
scoped repositories/APIs and legacy retirement are not implemented here. No new
business endpoint, ACTIVE status, enrollment, provider or dispatch change.

## Schema contract

Eleven existing business tables receive nullable application_id BIGINT with no
DEFAULT and a validated applications(id) FK, ON DELETE RESTRICT. They are users,
device_tokens, templates, segments, segment_members, notifications, upload_batches,
upload_batch_rows, notification_targets, notification_deliveries and notification_reads.
A partial application_id index covers assigned rows on each table. Legacy inserts
still produce NULL; **this intermediate schema is not application isolation**.

Users, device_tokens, templates, segments, notifications and upload_batches have
UNIQUE(application_id,id) parent keys. Device tokens also have
UNIQUE(application_id,id,user_id). These keys do not relax existing global
external_id/push_token uniqueness. Per-app subject namespaces and protected
provider-project address conflicts remain M03c; name uniqueness policy/final
scope constraints still need their own migration when scoped services are ready.

Composite FK additions, alongside the existing legacy FKs:

| Child | Scoped reference | Delete action |
| --- | --- | --- |
| device_tokens | user_id → users | CASCADE |
| segment_members | segment_id → segments; user_id → users | CASCADE |
| notifications | optional template_id → templates | SET NULL(template_id) only |
| upload_batch_rows | batch_id → upload_batches | CASCADE |
| notification_targets | notification_id → notifications; optional segment_id/user_id/upload_batch_id → respective parents | CASCADE |
| notification_deliveries | notification_id → notifications; user_id → users | CASCADE |
| notification_deliveries | (device_token_id,user_id) → device_tokens(id,user_id) | CASCADE |
| notification_reads | notification_id → notifications; user_id → users | CASCADE |

Every reference above includes application_id on both sides. The delivery triple
prevents attribution to another user within the same app. Every nonnull optional
target reference is constrained even when the old target CHECK permits extra
fields. Template deletion nulls only template_id and preserves application_id;
PostgreSQL documents selective SET NULL for composite references in its
[constraints reference](https://www.postgresql.org/docs/17/ddl-constraints.html).

MATCH SIMPLE permits unassigned legacy rows to bypass the new composite FKs.
An assigned child requires a matching assigned parent. NULL→owner is permitted
once; each table's immutable_application_owner BEFORE UPDATE trigger rejects
nonnull→other and nonnull→NULL with SQLSTATE 23514. Same-owner updates work.
This protects ownership under ordinary DML, not authorization or a privileged
schema owner: production runtime roles must not own tables, disable triggers or
perform DDL. Privilege separation/deployment remains an open security gate.

Down is one atomic DO statement: take ACCESS EXCLUSIVE locks on all eleven
business tables, reject any assigned row, then remove the new objects. A row
cannot be assigned between the guard and column removal. It preserves unassigned
legacy data and refuses to erase assigned ownership. Down does not undo a
reviewed backfill and is not a production rollback plan.

## Read-only preflight

`scripts/sql/application-scope-preflight.sql` is one count-only SQL statement,
for schema 000017. It reports assigned/unassigned counts on every business table,
delivery device/user conflicts, unresolved known business audit entities,
unclassified global audits and resolvable business audits awaiting scope.
It emits no IDs, names, emails, token addresses or audit snapshots. It is not a
green/red production readiness result: legacy NULL counts, unresolved audit,
consumer inventory and operational holds require explicit review.

## Operator backfill contract

`scripts/sql/backfill-reviewed-single-application.sql` is **operator-only** for an
explicitly reviewed one-app legacy dataset. It is not an automatic migration,
API, multi-app repair tool or production job. Neither script has been executed
against a real deployment. It requires schema 000017 and a quiesced maintenance
window including all APIs/background/direct writers and registry administration.

Use a SERIALIZABLE transaction and parameter-bound transaction-local settings:

```sql
BEGIN ISOLATION LEVEL SERIALIZABLE;
SELECT set_config('hello.scope_target_public_id', $1, true);
SELECT set_config('hello.scope_mapping_ack', $2, true);
-- Run count-only preflight, then the checked-in backfill SQL, then preflight.
-- Inspect counts/constraints and record reviewed evidence before COMMIT.
-- ROLLBACK on any uncertainty; never retry ambiguous commit blindly.
```

`$1` is the explicitly reviewed existing PROVISIONING public UUID; `$2` must be
`one-app-ownership-reviewed`. These are driver parameters, not literal psql
interpolation instructions. The script raises 23514 for missing acknowledgement,
ordinary isolation, missing/unknown target, any business row already owned by
another project, legacy delivery device/user mismatch or conflicting nonnull
known business audit scope. Invalid UUID syntax is a PostgreSQL input error.
No implicit first/only/default application is chosen. The acknowledgement asserts
an external human review; it does not prove ownership or authenticate an operator.

It takes fixed-order SHARE ROW EXCLUSIVE table locks on applications, all business
tables and audit_logs, then locks the selected application row. Locks block DML
while the script runs, but do not replace quiescence or prevent old writers from
resuming after commit. Set reviewed transaction/lock/statement timeouts in the
operator session; a timeout/deadlock/serialization error requires full rollback
and fresh preflight. No automatic retries or provider calls are performed.

Roots and parents are assigned first, then their children. All nonnull existing
business ownership must already match the target. Composite constraints enforce
all child/parent edges. Only NULL application_id fields are updated: business
content, timestamps, row IDs, token material and audit snapshots remain intact.
Known resolvable segment/notification audit rows are mapped to their verified
parent; global staff/registry audit is untouched. Deleted/unknown business audit
stays NULL, restricted from future app-facing access, with explicit unresolved
counts. This restricted legacy disposition still needs operator release review.

The DO statement is atomic. A failure late in assignment rolls back earlier
assignments and audit scope changes. Same-target reruns are idempotent. Record
operator identity, mapping evidence, script/version, before/after counts and
commit outcome outside this script; no fake staff actor or fabricated audit event
is introduced. Actual backup/restore, orphan preservation and consumer cutover
remain required. Do not print connection strings or snapshot JSON into evidence.

## Generated model compatibility and rollout

Generate sqlc only from migration/query sources. Adding a model column otherwise
turns former whole-model projections into generated Row types. The affected SQL
projections explicitly include application_id so repository signatures remain
stable; no optional scope filter or service interface changes are introduced.
The current legacy queries remain global and **must not be reused as scoped
queries**. The inventory checker still enumerates the same 135 query names.

Apply 000017 before deploying the new binary because those projections and model
scans now require application_id. Test upgrades and rollback on disposable data
first. Do not run backfill or introduce a second app's business rows into a live
legacy stack. Local rollback-only SQL fixtures may use A/B assigned rows without
starting server/worker/scheduler. All registry projects remain PROVISIONING.

## Acceptance and remaining work

Integration must exercise clean migration up/down/up and populated expansion
with every table, absent defaults, count-only preflight, missing/wrong mapping
inputs, conflict rejection, late failure atomic rollback, idempotent backfill,
audit preservation, owner move/clear rejection on eleven tables, cross-app FK
inserts, same-app wrong-user/device rejection, template selective nulling,
cascades, retained global uniqueness and refusal of destructive down after mapping.
Existing registry migration tests roll back 000017 inside their disposable
transaction before testing 000016. No fixtures may commit app-B business rows or
start the old worker against them.

This is M03b.2a, not completion of all M03b.2: final NOT NULL/scoped keys must be
coordinated with the scoped repository/API transition. M03b.3/4 must implement
those callers before finalization/legacy retirement in M03b.5. Production mapping,
privilege separation, restricted unresolved audit and rollout evidence stay open.
