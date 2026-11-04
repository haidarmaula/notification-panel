# M05 replay publication and expired-claim recovery

Date: 2026-11-04. Scope: `ReplayExecutor`, `ReplayRecovery`, conditional SQLC
queries and isolated fixtures. These extend [claim transactions](m05-replay-claim-verification.md).
The libraries remain unwired; this is not a production replay command or rollout.

## Publication

- `ReplayOne` composes claim and publication with a 15-second overall bound.
  Publication has a five-second transaction and a two-second publisher call.
  It repeats pinned credential verification, current DB staff/send-role/app/grant
  authority, full selection scope, immutable header, acceptance/epoch/hold checks
  and active claim ownership. A final combined DB-time check requires five seconds
  remaining on effective credential/MFA validity, operational grant and lease.
- Prepared/terminal work becomes an admission no-op; new holds/expiry/rejection
  do not publish or reset accepted work. Original key, epochs and deadlines are
  retained. The publisher receives a validated event, never raw poison bytes.
- A shared atomic flag consumes the in-memory proof before the first publisher
  invocation. Copied results, a rollback, lost commit acknowledgement or a later
  invocation cannot reuse it for external I/O. Existing committed results can
  be read back without another publisher call. No I/O transaction is retried.
- Broker ack commits `published`, resolution and `NOTIFICATION_REPLAY_RESULT`
  audit together. Error, panic or timeout becomes `publish_unconfirmed`, releases
  the claim with audit and retains unresolved raw evidence. Errors expose no
  broker diagnostics. A canceled/failed DB result transaction reports no success;
  durable claim evidence remains recoverable.

## Recovery

`ReplayRecovery.Tick` discovers 1–100 expired active claims with a five-second
read-only metadata transaction, then reacquires canonical runtime/source/app/
staff/grant/request/policy/campaign/control/evidence locks per candidate. The
whole tick is bounded to 30 seconds; each candidate gets at most five seconds,
with independent one-second rollback cleanup. Counters retain acknowledged
progress when a later candidate fails; callers own polling and error backoff.

Cleanup uses database expiry and exact active token predicates. It records
`abandoned_unknown`, clears only that claim and writes
`NOTIFICATION_REPLAY_ABANDONED` atomically. It never publishes, resolves raw
evidence, terminalizes campaigns or recreates authority. It works while sealed,
source/app inactive or original staff/grant unusable. Audit retains original
request actor provenance and names `expired_claim_recovery` as executor. Missing
historical grant provenance is not adopted as execution permission. A stale
candidate cannot clear a successor's claim.

## Pinned Kafka cancellation contract

The pinned `github.com/segmentio/kafka-go` v0.4.51 writer can return on cancellation
while producer-owned queued/in-flight work continues. Produce has its own write
timeout. BatchSize=1, MaxAttempts=1 and per-attempt timeouts do not establish a
global queue-drain deadline. Message buffers remain immutable. A caller timeout
or released database lock does not prove that all broker I/O stopped.

Stop new calls and drain the producer with Close before source/epoch handoff or
restore. The local once-per-proof guard and durable preparation dedup protect
uncertainty; they do not provide remote exactly-once delivery. Deployment wiring
must exercise canceled writes, backpressure and shutdown/drain separately.

## Evidence

- Full unit/race, build, vet (including tagged integration code) and format pass.
  No module dependency/version changed.
- Full PostgreSQL/Kafka suite passes in approximately 76 seconds, including all
  27 migrations up/down/up and a real 30-second lease expiry without disabling
  immutable guards or adding a production clock override.
- After tightening the final combined validity check, focused publication,
  recovery, real Kafka/dedup and generation-handoff fixtures pass on a fresh
  broker. A slow-DB regression consumes the validity margin and proves zero
  publisher invocations.
- Fault cases cover publisher error/panic/timeout, revoked grant, new policy hold,
  preparation since claim, audit rollback, broker ack with rolled-back DB commit,
  actual commit with lost acknowledgement, no-proof replay, expired predecessor
  versus a successor, and recovery audit rollback with partial progress.
- A real scoped producer publishes to a unique Kafka topic; the validated event
  reaches Preparer. A second real event after adding a live user retains the
  original known-empty snapshot and does not add intents. Broker cluster metadata
  is real; the incarnation manifest is a fixture assertion, not production proof.
- A preexisting fresh-broker fixture race surfaced `NotCoordinatorForGroup`.
  Its readiness now checks actual OffsetFetch and both requested partitions,
  rather than coordinator discovery alone. Run errors and unsafe-offset assertions
  are still surfaced. Fresh-broker focused verification passes.
- The existing Firebase SDK HTTP simulation remains local. No real provider/device,
  operational MFA issuer, production source manifest or hosted CI is verified.
- Reachable vulnerability scanning reports zero affected vulnerabilities; one
  imported-package and three module findings remain on uncalled paths.

Independent read-only review found no blocking authority, token, atomicity or
cleanup issue. The literal pre-publication margin was strengthened after review.

## Remaining gates

Add a restricted CLI and configuration/issuer integration, then wire the modern
worker/scheduler under reviewed lifecycle, source manifest, TLS/SASL/ACL, drain
and rollout controls. M05 is not closed by this library slice. M06 FCM execution
and M07 receipt/statistics contracts remain separate work; replay publication
acknowledgement means neither preparation nor provider/device delivery.
