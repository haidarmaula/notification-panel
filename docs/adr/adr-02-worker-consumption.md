# ADR-02 — Serial durable admission, safe Kafka commits and dead-lettering

**Status:** proposed, revised 2026-11-02 following
[D1/D6/D7 review](../analysis/adr-01-02-review.md). Not implemented or accepted.
NEW companion SPEC: `docs/specs/adr-02-worker-consumption.md` (not yet written).

**Scope:** cmd/worker, NEW internal/worker, dead-letter repository and sqlc,
migrations/configuration, NEW cmd/requeue, operator documentation. No new topic.


Cross-ADR protocol: [consistency review](../analysis/adr-01-11-consistency-review.md).
Its shared lock/identity rules and verification gates apply to this proposal;
review completion does not mean acceptance or runtime implementation.

## Background and dependencies

`cmd/worker/main.go` currently fetches, processes and commits in one goroutine;
on processor error it continues fetching. The pinned kafka-go v0.4.51
Reader.CommitMessages documentation states that committing a higher offset
also commits all lower offsets in that partition. Separate commit calls are
not independent acknowledgements. The current PROCESSING-with-rows guard is
not a complete idempotency or recovery guarantee.

[ADR-07](adr-07-kafka-topology.md) also identifies that current LeastBytes
does not provide key-based partition affinity (H47). Replay routing claims
below require its corrected hash contract and reviewed mapping transition;
deterministic key bytes alone are insufficient.

ADR-01 replaces combined processing with durable preparation and separate
provider drivers. ADR-03 supplies the durable QUEUED+outbox transaction.
ADR-07/10 cover retention/topology. Topic retention must exceed the agreed
outage/recovery window; uncommitted offsets cannot recover records already
removed by broker retention. Confirmed scale is in the
[production plan](../analysis/production-readiness-plan.md).

## A-1. One admission message in flight per reader

### Context

Concurrent completion can move a partition offset past unfinished work. It
does not accelerate provider submission of one large campaign.

### Decision

One consumer loop fetches exactly one message, validates it, prepares it via
ADR-01 A-10, persists quarantine if needed, then commits before fetching the
next message. No provider calls on this path, no admission pool, no callbacks
that commit later. The FCM pool consumes durable DB intent independently.

Validate JSON plus required positive notification ID, supported envelope fields,
timestamp shape and canonical key/ID agreement. Syntactically valid but invalid
events are quarantine candidates, not fake zero-audience success. Unknown
notification/unsupported legacy targets carry explicit reasons for inspection.
Validation ownership and forward-compatible fields are fixed in the SPEC.

[ADR-10](adr-10-retention-recovery.md) adds a deployment-approved source_epoch
and immutable origin_epoch to the modern send envelope. Validate source_epoch
against active/reconciled lineage, then match (origin_epoch, notification_id)
to the accepted row before execution. Unexpected old/future epoch
is quarantined/reconciled, never executed against a reused restored row. The
legacy-to-modern envelope rollout is explicit; a missing field is not silently
upgraded to the active epoch. Ordinary restarts retain the epoch.

For admission DB errors, retry the same message with jitter within proposed
WORKER_MAX_ATTEMPTS=3 and WORKER_RETRY_BUDGET=90s, per-invocation timeout 30s,
base delay 1s, cap 16s. Stop at the earlier budget/attempt bound. None of these
calls may continue after cancellation to mark success asynchronously. Exhaustion
attempts durable quarantine. Deadline cancellation during shutdown is not poison.

### Alternatives rejected

Continue-without-commit plus later success can lose the earlier event. Parallel
admission needs a per-partition contiguous commit coordinator and generation
handling, which is deferred until benchmarks show admission is a bottleneck.

### Consequences

A transient failure may block this reader, including its other partitions.
That is an intentional correctness/complexity trade-off at this workload.
Provider delivery on already-prepared work continues while admission retries.

## A-2. Durable dead-letter, with fail-closed behavior

### Context

Malformed/exhausted events need durable evidence. PostgreSQL may be unavailable
at the same time that admission and dead-letter storage fail.

### Decision

NEW notification_send_dead_letters retains source topic/partition/offset,
original key/raw bytes, validated notification ID when available, decoded
payload, bounded error class/message, attempts, creation and resolution metadata.
UNIQUE(log_source_id, original_partition, original_offset) makes redelivery
after insert-before-commit idempotent. log_source_id identifies cluster plus
topic incarnation, not topic name alone: recreating a topic can reuse offsets.
Provisioning records a verified topic ID or explicit generation when the client
cannot supply it. A changed/unknown incarnation fails closed for reconciliation.
Malformed raw keys are BYTEA, not assumed UTF-8 text. Raw payload is sensitive;
access control, size bounds and retention are specified with ADR-10/11.

Write success makes the current message safe to commit. If quarantine insertion
fails, **stop fetching and do not commit anything past this message**. Retry
that insertion with bounded per-call timeout and capped jitter delay until the
database recovers or process cancellation ends the loop. Rate-limit logs and
raise an alert. The per-message admission retry budget does not authorize
discarding evidence when its dead-letter write fails.

A process exit leaves the unsafe offset uncommitted. Recovery still depends
on Kafka retention and operational availability; this is not a guarantee of
survival through arbitrary storage loss. Do not turn dead-letter into a
terminal campaign failure. ADR-01's QUEUED/PROCESSING recovery may independently
prepare a valid notification after the database returns.

### Alternatives rejected

Moving to the next message after failed dead-letter reintroduces D1. A Kafka
DLQ could retain evidence during DB downtime but adds another dependency;
choose the PostgreSQL store with explicit reader backpressure for v1. It is
incorrect to claim a Kafka DLQ inherently loses messages merely because the
database is down.

### Consequences

No unbounded in-memory queue accumulates during DB outage. Backlog remains in
Kafka. Queryable quarantine and alerting are part of delivery operations.

## A-3. Commit, rebalance and shutdown policy

### Context

Safety is a contiguous prefix of durable messages per partition, not a set
of individually acknowledged messages.

### Decision

Only commit the current message after complete durable fan-out, terminal
no-op, or successful durable quarantine. No next FetchMessage until this commit
succeeds. On commit error retry it under a bounded context; if the reader/group
cannot make progress, stop/cancel/close this reader and restart it with capped
backoff under the process supervisor. Never process subsequent messages in
the failed reader to bypass the unresolved commit. Redelivery is safe because
preparation and quarantine are idempotent.

Serial handling maintains a safe contiguous prefix by construction; there is
no numeric offset+1 algorithm (Kafka offsets may have gaps). Admission remains
serial even after provider pool concurrency is enabled. Any future concurrent
admission revision must add an explicit partition completion coordinator.

Use reader/group lifecycle with no detached commit callback or goroutine from
a previous reader. Rebalance cancellation rolls back preparation that has not
committed. If DB commit precedes assignment loss, a new consumer may replay;
the notification lock/completion marker makes that safe. Validate pinned
Reader generation/commit-error behavior with broker integration tests before
relying on it in the SPEC. An active older provider task is controlled by its
DB lease, independently of Kafka assignment.

On shutdown stop fetching, cancel contexts and local wakeups, allow bounded
drain of current DB operations, commit only a proven-safe current message when
possible, then close the reader. Never commit a partial fan-out because a
shutdown deadline elapsed. Cancel/join provider drivers and release their own
leases if safe; otherwise leave leases to expire. SIGTERM timing cannot promise
every provider result is recorded before exit.

### Alternatives rejected

Commit-on-fetch loses work. A higher offset from another goroutine is not a
safe retry of a failed commit. Kafka transactions alone do not atomically
commit PostgreSQL intent or external FCM effects.

### Consequences

The existing deterministic producer key is unchanged. Serial admission favors
correctness; healthy provider throughput is independent of reader stall.

## A-4. Operator replay and retention

### Context

Replay must retain enough evidence and cannot restore the original log order.

### Decision

NEW cmd/requeue requires explicit dead-letter IDs and actor/operator identity,
validates decoded notification ID and canonical key, reports invalid rows
without publishing raw malformed bytes, and publishes a **new** send event
using the existing notification_<id> key. Preserve the validated original
origin_epoch/source_epoch; never stamp the current epoch onto an old record.
An explicit reviewed recovery workflow is needed for a lineage transition.
This routes consistently only while
partition count/partitioner are unchanged; it does not restore old ordering.

Record replay attempt/result and source row identity. After broker ack mark
resolved/replayed (do not immediately erase audit evidence); failure retains
unresolved evidence. Crash after publish before resolution may repeat the
event; durable preparation absorbs it without budget reset or new audience.
An already-terminal notification is an explicit no-op, not a resend action.
Concurrent operators use a row claim/lock with bounded context. Resolution
updates need idempotency; replay lease/schema details are proposed in the
[operator reprocessing SPEC](../specs/m05-operator-reprocessing.md), not yet implemented.

### Alternatives rejected

Deleting every row after replay loses operational history. Claiming an original
key reconstructs historical sequence is incorrect. Automatically replaying
poison raw bytes indefinitely creates a hot loop.

### Consequences

Retention and user/PII deletion follow ADR-10. Operator replay is distinct from
automatic due-delivery recovery and product-level resubmission of FAILED.

## A-5. Configuration and implementation boundary

### Context

Admission retry differs from provider retry, lease timing and broker retention.

### Decision

Worker-owned proposed config: WORKER_MAX_ATTEMPTS=3,
WORKER_RETRY_BUDGET=90s, WORKER_ADMISSION_TIMEOUT=30s,
WORKER_RETRY_BASE_DELAY=1s, WORKER_RETRY_MAX_DELAY=16s. Bound DLQ write,
commit and close operations with explicit timeouts in SPEC. Validate positive
durations and relationships. Distinguish blocked-on-storage from an unhealthy
process in readiness/metrics (ADR-08); liveness must not induce restart storms.

NEW internal/worker owns injectable admission loop, clock/sleeper, quarantine
and reader seams. NEW repository/query/migration pair for dead letters and
NEW cmd/requeue; migration numbers selected at implementation time. Generated
sqlc must be regenerated after migrations. Update feature mocks if their
interfaces actually change. API contracts remain unchanged by this ADR.

### Alternatives rejected

One retry budget for DB admission and provider delivery conflates different
durability and timing rules. A claimed 15s total stall counted only sleeps,
ignoring operation deadlines and an unavailable dead-letter database.

### Consequences

No runtime finding is marked fixed by this revision. Deployment needs retention
and operator access decisions before release. Exact Reader lifecycle tests and
quarantine limits are required before implementation acceptance.

## Design verification cases for the companion SPEC

| Input / failure | Condition | Expected observable result |
| --- | --- | --- |
| Offset 10 unsafe | Admission and quarantine fail | No fetch/commit of offset 11; current raw bytes retained in bounded memory |
| Same outage then restart | Offset 10 still within retention | Offset 10 replayed, eventually durable; no silent skip |
| DLQ insert then crash | Offset not committed | One quarantine row for source position; redelivery safely commits |
| Commit fails after fan-out | DB already committed | No new fetch; commit retry or reader restart; replay is admission no-op |
| Rebalance during preparation | DB tx not committed | Rollback or one complete snapshot; no partial audience acknowledged |
| Rebalance after DB commit | Another consumer reads event | Same completed snapshot, no duplicate intents |
| Malformed or invalid envelope | Notification ID/key untrusted | Quarantine with evidence; no arbitrary provider dispatch |
| Shutdown before safe state | Drain deadline elapsed | No unsafe commit; reader closes and message remains replayable |
| Replay succeeds then CLI crashes | Resolution not committed | Repeated event harmless; audit retained and eventual resolution possible |
| Terminal notification replay | Already COMPLETED/FAILED | Explicit no-op; no budget reset or provider I/O |
| Provider tasks run concurrently | Serial admission active | No provider callback can advance Kafka offsets |
