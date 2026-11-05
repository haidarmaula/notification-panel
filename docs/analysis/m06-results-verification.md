# M06 result persistence verification

Date: 2026-11-05. Scope: `Execution.RecordResult`, migration 000030 and full v2
projection storage. [Contract](../specs/m06-results.md). No update publication or
delivery worker activation is included.

Unit/race tests cover success/early observation preservation, permanent and local
payload failure, uncertainty, DB/provider/quota floors, expiry and budget exhaustion,
plus projection validation and serialization. Focused review found no additional
concrete fencing, lock-order, idempotency or atomicity defect. A separate focused
red/green regression caught retry hints rounded below their provider floor; next
at now rounds up to database microseconds. See RR1 in the review record.

Focused PostgreSQL/actual-SDK/Postman race tests pass in 12.060 seconds. They cover:

- Exact one-version/event persistence, byte-stable duplicate acknowledgement and
  no event recreation after dispatched-row deletion. Immutable payload edits fail.
- Real commit with lost acknowledgement returns zero success evidence; retry
  acknowledges the committed result. Outbox insertion failure rolls back status,
  token retirement and version advancement; retry persists once.
- UNREGISTERED retires only the original current binding. Payload/sender/auth/APNs
  failures never purge tokens; configuration failures atomically hold new dispatch.
  A refreshed replacement binding and its installation version remain intact.
- Quota/availability retain PENDING and consume no projection version/event, while
  recording an eligible retry floor. Lease replacement/expiry, epoch replacement,
  runtime hold and version overflow reject writes.
- App/new-dispatch holds preserve in-flight acceptance, late confirmed acceptance
  can be recorded after campaign deadline under a live lease, and early OPENED
  remains OPENED. Retained evidence prevents destructive down migration.

The complete isolated race suite passes: `hello/tests/integration` in 109.605
seconds and `hello/internal/fcm` in 2.729 seconds. This includes all 30 migration
up/down/up checks, post-submission logout, budget/deadline finalization and rejection
of an old result after a new attempt is reserved. Simulation logs show 50 completed
FCM requests across the expected 200/400/401/403/404/429/503 scenarios and three
OAuth requests. Fixture containers/volumes are removed afterwards. Full unit/race,
build, integration vet, format and module verification pass. Vulnerability scanning
reports zero affected call paths; one imported-package and three uncalled-module
advisories remain.

No production credential/device is used. This proves local transactional behavior
against synthetic provider responses, not real IAM/APNs delivery or capacity.
Ordered update publication, consumer version/dedup acceptance, retry/recovery/
finalization loops, M07 receipts/statistics and delivery composition remain open.
