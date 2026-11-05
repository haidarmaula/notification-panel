# Runtime composition foundation verification

Date: 2026-11-05. Scope: authenticated Kafka admission, source-bound outbox,
bounded schedule discovery/promotion, process loops, scheduler composition and
protected worker/scheduler executable wiring.
Contract: [runtime composition](../specs/m04-m05-runtime-composition.md).
The actual daemon entry points/configuration are now wired; this is not a
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
  tasks/fetchers join before their daemon drains the publisher and closes its pool.
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

The executable-boundary follow-up extracts the existing trust primitives into
`internal/deployment` and strict object decoding into `internal/jsonobject`.
`cmd/worker` and `cmd/scheduler` now use independent exact schemas without replay
issuer/key/grant requirements. Worker explicitly rejects delivery mode and runs
admission-only. Configuration loading rejects role confusion, unknown/duplicate/
null fields, unsafe bounds and inherited DB/root-certificate overrides.

The composed fixture now loads the actual per-role daemon configurations and
runs their pool/source/transport lifecycle through verified PostgreSQL TLS and
Kafka mTLS. Wrong source incarnation/partition count, untrusted broker CA and
untrusted DB CA all fail startup. After cancellation, no daemon DB sessions remain.
The local fixture SQL role is a disposable superuser for isolated schema setup;
it does not establish production least-privilege grants. It uses trusted streams,
not host `/etc` files. The TLS pipeline plus actual replay-command regression and
SDK simulation pass with the race detector (23.158 seconds).

The complete isolated integration rerun passes with the race detector in
101.900 seconds, including all 27 migration up/down/up checks and the SDK
simulation. Native unit/race, build, vet (including integration code) and
formatting pass. Full-pipe help/diagnostic tests prove output
cancellation unblocks process shutdown (DR1). Linux ARM64 protected-file smoke
runs all three actual executables offline in a disposable container: replay reaches
credential rejection, both daemons accept their own root-owned policy and reach
expected network-unavailable startup failure. No host `/etc` files are created.
The extracted Linux ACL regression also passes. Synthetic credentials only.

The reachable-vulnerability scan reports zero affected call paths; one imported
package and three uncalled module advisories remain. Module files are unchanged.
Provider execution, provider recovery, receipts/statistics and production
acceptance remain subsequent milestones.
