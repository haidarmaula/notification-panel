# Runtime composition foundation verification

Date: 2026-11-05. Scope: authenticated Kafka admission, source-bound outbox,
bounded schedule discovery/promotion, process loops and scheduler composition.
Contract: [runtime composition](../specs/m04-m05-runtime-composition.md).
The actual daemon entry points/configuration remain unwired; this is not a
production activation or M06 provider executor.

## Local evidence

- Authenticated admission negative policy and immutable-copy tests pass with the
  race detector. The two-partition/two-member handoff fixture now also passes
  through the real mTLS listener: generation revocation, no unsafe offset commit,
  durable poison evidence, immutable snapshot/intent dedup and joined fetchers.
- A real PostgreSQL red test reproduced stale-origin schedules filling the first
  due page. After the SQL update, old origin/source epochs are excluded, sealed
  runtime produces no candidates, and expired/migration-held accepted schedules
  remain eligible. A second-transaction failure retains the first committed
  promotion; retry processes only pending work and expires the final candidate.
- The source-bound outbox holds inactive registration without invoking its
  publisher or changing attempts. Reactivation permits publication using the
  original copied manifest. Canonical runtime -> source -> outbox locks remain
  held through publication/result handling.
- Loop tests cover backoff bounds/redaction, callback deadlines, cancellation,
  invalid dependencies and waiting for sibling cleanup. No timeout goroutine
  abandons a callback or its resources.
- The composed scheduler/worker fixture runs all four polling tasks plus
  supervised authenticated admission. A real authorized schedule becomes an
  outbox, crosses Kafka mTLS and reaches a complete one-device snapshot with
  attempt_count=0/delivery_version=0. Its accepted deadline remains unchanged,
  the outbox empties, and exactly one message position is committed. Runtime
  tasks/fetchers join before the caller drains the publisher and closes the pool.
- Full isolated integration passes in 98.161 seconds with the race detector and
  all 27 migration up/down/up checks. The existing Firebase SDK simulation records
  statuses 200/404/429/503; no real FCM/APNs/device credentials are involved.
- Full unit/race, build, vet (including tagged integration code), formatting and
  reachable-vulnerability scan pass. The scan reports zero affected call paths,
  with one imported-package and three uncalled module findings still present.
  Module/version files are unchanged; existing module verification remains valid.

SQLC was regenerated from the query; no generated file was edited by hand.
This is a query-only change, so no schema migration was added. Independent
read-only review found no concrete new authority, concurrency, lock-order,
cleanup or private-error logging fault. SR1 is recorded in
[the code review](../../code-review.md).

## Evidence limits and next work

Fixtures use one broker, synthetic app/installation identities and a declared
incarnation; metadata still cannot independently verify a topic UUID. The tests
establish local composition, not production HA, capacity, real issuer/consumer
integration or device delivery.

Next share the tested protected-policy/DB/TLS parsing primitives with daemon
configuration, then wire actual worker/scheduler startup, supervised Run, signal
cancellation and publisher drain before pool close. These processes must not
require replay operator keys/grants. Provider execution, provider recovery,
receipts/statistics and production acceptance remain subsequent milestones.
