# M03a.3 — Provisioning registry HTTP API

2026-11-03. [SPEC](../specs/m03a-application-registry.md),
[API guide](../api/applications-api.md), [OpenAPI](../contracts/application-registry.openapi.json).

## Result

The server composes the applications module's ten registry/grant operations.
The global management surface requires current SUPER_ADMIN authority; all four
known roles can read their explicitly granted metadata. SUPER_ADMIN has no
implicit membership. API key, staff JWT, active-account lookup, route DB role
check and independent service checks remain in the real composition. Existing
legacy business routes and worker/scheduler behavior are unchanged.

HTTP uses allowlisted DTOs: public app UUID, decimal-string staff IDs/revisions,
PROVISIONING metadata, UTC timestamps; no internal app row ID or staff secrets.
ETags and creation Locations follow the contract. Conditional writes map missing
conditions to 428 and stale conditions to 412. DELETE soft-revokes and returns an
empty 204. Every registered operation sets no-store outside its auth middleware,
including denials and internal failures; mux-generated unknown path/method errors
retain existing server behavior.

New write bodies opt into strict JSON: exact case-sensitive top-level fields,
required non-null values, UTF-8, single object/value, decoded-key duplicate
rejection and a complete 64KiB read budget. Duplicate detection bounds nesting
to 64 containers. Content type/charset/encoding rules return 415. DELETE accepts
only an empty bounded body. Shared bounded reading was extracted without
changing legacy DecodeJSON's compatibility semantics. Unknown errors always use
literal internal server error; wrapped domain errors return constant messages.

## Validation

Passed locally:

- `go test ./...`, `go build ./...`, `go vet ./...`.
- `go test -race ./internal/httprequest/... ./internal/features/applications/... ./internal/authz/...`.
- `go vet -tags=integration ./tests/integration`.
- `bash scripts/ci.sh format` and `git diff --check`; new files also gofmt'd.
- `bash scripts/integration.sh`, exit 0: all tagged tests pass in 17.191s,
  including the new HTTP suite in 0.29s, registry/service concurrency and audit
  rollback suites, migration up/down-all/up, existing repository/Kafka smoke and
  Firebase SDK simulation. Nine actual SDK HTTP requests: 200×2, 404×1, 429×1,
  503×5. Fixture containers/network were removed by the runner.

Unit/build/vet/race logs: `/tmp/hello-m03a3-{unit,build,vet,race}.txt`;
final integration log: `/tmp/hello-m03a3-integration-final.txt`.
The SDK simulation is retained baseline coverage; the new registry makes no
provider calls. Unit race checks do not instrument the real-DB integration suite.
No dependency/SQL migration changes; vulnerability scanning was not repeated.

The new HTTP integration suite uses real API-key/JWT/audit middleware, PostgreSQL,
repository queries and transaction services. It covers all ten routes, all four
roles on membership reads, three denied roles on all eight management operations,
missing authentication/wrong API key, stale token roles, explicit super membership,
A/B metadata visibility, no implicit create grant, safe DTOs, exact-key filtered
counts/empty pages, ETag rename/no-op/stale transitions, first grant and revoke/
regrant ABA, empty 204, strict invalid inputs leaving full DB snapshots unchanged,
inactive staff/current demotion and a real closed-pool failure returning safe500.
The previous service integration suites retain concurrency, transaction authority,
audit rollback and retry evidence; HTTP tests do not claim a second concurrency model.

Unit tests add strict media/body/UTF-8/duplicate/case/depth handling, exact and
oversized known/unknown-length reads, DELETE limits, conditional-header parsing
and wrapped-domain/private-error mapping. Existing legacy decoder tests pass.

The initial integration run failed one new test expectation: If-None-Match:* on
an existing grant correctly returned 412 before target-status evaluation. The
inactive-target case was corrected to send its current ETag; implementation was
unchanged. An initial direct tagged test command without opt-in was rejected by
the fixture guard; the authoritative integration run uses scripts/integration.sh.

## Boundaries

This completes the locally verified registry HTTP slice. All project
rows remain PROVISIONING; no app-B users/devices/campaigns, activation, enrollment,
provider credentials, provider adapter or mobile changes are added. M03b must
scope/backfill existing data and queries and scope or retire/drain all legacy
APIs, worker, scheduler and fanout before second-app business data. The old
pipeline still sends through OneSignal. No production readiness, aggregate
capacity, actual FCM/APNs/device delivery, hosted CI or infrastructure HA is
established by these local checks. M02 production controls remain open.
