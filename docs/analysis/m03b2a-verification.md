# M03b.2a — Business ownership expansion verification

2026-11-03. Implemented and locally verified.
[Exact schema/tool contract](../specs/m03b2-business-scope-expansion.md).
This is the additive part of M03b.2, not completion of business application isolation.

## Result

Migration 000017 adds nullable/no-default app ownership to all eleven legacy
business tables, app FKs/indexes, parent composite keys, all child app-qualified
references and the delivery/device/user triple. Existing conservative global
subject/address uniqueness remains. Eleven triggers prohibit moving/clearing an
assigned owner under ordinary DML. Selective template SET NULL keeps ownership;
existing delete cascades are retained. Down locks all business tables, checks for
assigned rows and removes schema in one atomic DO statement; assigned ownership
cannot be silently stripped.

Count-only preflight and an operator-only reviewed one-app backfill are checked
in as SQL tools. Backfill requires explicit PROVISIONING UUID, acknowledgement,
SERIALIZABLE transaction and maintenance quiescence. It rejects foreign assigned
business rows/audit and wrong delivery device/user ownership, assigns parents
before children, preserves business data/timestamps/snapshots, scopes resolvable
segment/notification audit and retains unresolved/global audit. Same-target reruns
are idempotent. These tools were exercised only against disposable fixtures,
never a real deployment. No provider, mobile or credential access was involved.

Generated model projections now carry the nullable ownership field without
changing existing repository signatures. SQL sources and migration were changed
before regeneration; generated code was not edited manually. No mocked interface
changed, so mockery regeneration was unnecessary. Existing queries remain global.

## Passed checks

- `go test ./...`, exit 0 (`/tmp/hello-m03b2-unit-final.txt`).
- `go build ./...`, `go vet ./...`, exit 0 (build/vet-final logs under `/tmp/hello-m03b2-*`).
- `go vet -tags=integration ./tests/integration`, exit 0.
- `bash scripts/integration.sh`, exit 0: full suite 14.956s; new migration/backfill
  suite 0.17s. Final log: `/tmp/hello-m03b2-integration-verified.txt`.
- Integration runner applies 17 migrations up/down-all/up, runs prior registry,
  service/API and repository/Kafka/SDK suites and removes temporary containers.
  Nine SDK-to-local-simulation requests remain baseline coverage: 200×2, 404×1,
  429×1, 503×5. They are not real FCM/APNs delivery or tests of a new provider.
- Regeneration with sqlc v1.31.1 is reproducible (hash comparison across generated
  Go files), inventory still matches 16 tables / 135 query names / 46 routes /
  four binaries, formatting and diff whitespace pass.

The new rollback-only PostgreSQL test populates all eleven tables before expanding
and round-trips down/up without changing business data. It verifies missing
mapping inputs, unknown app, foreign assigned rows, inconsistent legacy device/
user, full rollback on an injected CHECK at the last business UPDATE, preserved
content/timestamps/audit snapshots, idempotent re-execution and unresolved audit
retention. It checks move/clear rejection on all eleven tables, cross-app new-row
FK failures, same-app wrong-user device failure, extra target references, template
selective nulling, original cascades/global uniqueness and guarded down rejection.
A/B business rows exist only in an explicitly rolled-back test transaction; no
legacy server/worker/scheduler runs against them. Registry remains PROVISIONING.

Initial compile checks caught whole-model projections becoming generated Row
types after adding the column; explicit SQL ownership projections fixed that.
One FK fixture initially collided with the old membership unique key first; it
was corrected to use a distinct foreign user so the app FK is tested directly.
Final checks above pass. No vulnerability scan was repeated (dependencies did not
change), no race-instrumented DB test or production concurrency/load test ran.

## Remaining gates and next slice

Apply migration 000017 before deploying the generated-model/query binary. This
changes database expectations and takes maintenance locks; review the exact SPEC
and rehearse on a restored copy before production. SQL scripts are operator
utilities, not authenticated application endpoints or automatic migration jobs.
Privilege separation and proof all writers are quiesced remain operational gates.

M03b.2 remains open for final NOT NULL/scoped identity/name constraints and approved
production mapping. Those final constraints must be coordinated with scoped
callers; applying NOT NULL now would break the old insert paths. Next implement
M03b.3 scoped user/device metadata and segment/member queries/services, with exact
API/permission contracts. Then cover remaining aggregates/projections, finalize
constraints and retire all 24 legacy business routes and global background paths.
Do not introduce app-B business data into a live stack. No production readiness,
activation, durable acceptance, address ownership proof or FCM switch is claimed.
