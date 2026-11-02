# ADR-03 — Atomic send acceptance and transactional outbox

**Status:** proposed, revised 2026-11-02 to align with ADR-01 A-8 and ADR-02.
Not implemented or accepted. NEW companion SPEC:
`docs/specs/adr-03-transactional-outbox.md` (not yet written).

**Scope:** notification send service/handler/module, server wiring, scheduler,
repositories/sqlc/migrations, configuration and API/database documentation.
No new Kafka topic. Worker delivery/ownership is ADR-01/02.


Cross-ADR protocol: [consistency review](../analysis/adr-01-11-consistency-review.md).
Its shared lock/identity rules and verification gates apply to this proposal;
review completion does not mean acceptance or runtime implementation.

## Background and dependencies

Current NotificationService.Send claims DRAFT->QUEUED then publishes to Kafka,
with best-effort rollback on publish error. A crash in between leaves no
durable publication intent. Scheduler processScheduled has the same gap and
attempts rollback using UpdateStatusIfScheduled after the row is already
QUEUED, so the rollback matches nothing.

This ADR and ADR-01 A-8 define **one** acceptance transaction, not a key
transaction followed by a claim transaction. ADR-02 owns commit after complete
fan-out; outbox ack is not proof of consumer admission. ADR-01 recovery covers
valid QUEUED notifications not yet prepared, including quarantine after publish.
ADR-05's update outbox must be reviewed against the revised send-drain protocol
before reusing it. Latest scope is the [production plan](../analysis/production-readiness-plan.md).

## A-1. One durable acceptance transaction

### Context

Status, replay result and publication intent must agree even if the process
dies or the HTTP response disappears.

### Decision

For immediate sends: authenticate/authorize the actor, then begin one bounded
PostgreSQL transaction. Reserve/lock the actor-scoped idempotency key, claim
DRAFT->QUEUED, persist queued_at/deadline_at, insert notification_send_outbox,
and store the immutable idempotent response **in that transaction**. Use
ADR-01 A-8's key namespace/expiry/locking contract; after required authority
locks, key precedes notification under shared CR1. On claim/insert failure roll
back everything. Replay may only
return committed acceptance. Unknown commit outcome is resolved by retrying
the same key, never by an out-of-transaction compensation.

ADR-09/11 require service-level operation permission (including scheduling),
current-authority/version validation and a sanitized audit record in this
transaction. Audit failure rolls the critical mutation back; no best-effort
post-commit audit is treated as the accepted target contract.

Persist ADR-10's immutable origin_epoch (accepted business identity) and approved
source_epoch (publication/recovery lineage) in the send outbox/envelope. Neither
can be inferred from whichever worker happens to replay it later. Sealed recovery
blocks admission/drain outside restored DB flags until epoch/outcome/ID
reconciliation is complete. Normal restart does not create a new source epoch.

ADR-06 adds a global migration hold and full-broadcast coverage preflight.
After checking for an authorized committed replay, reject a **new immediate**
send under global hold with proposed 503 and Retry-After, or insufficient
coverage with mapped 409, without committing key/status/outbox. Replays of
previously accepted sends still return their stored 202. Use bounded consistent
DB checks and document the new domain errors; no raw driver error is exposed.

For scheduled sends: persist scheduled_for + PUSH_TTL as deadline when an
authorized schedule is committed/edited. Promotion locks and rechecks the row.
If already expired, atomically finalize SCHEDULED->FAILED/ttl_expired with durable
lifecycle evidence, no send outbox, no invented audience/device results. Otherwise
claim SCHEDULED->QUEUED, persist actual queued_at and insert outbox atomically,
preserving the scheduled deadline. No HTTP key is required; skip a lost claim.
Do not publish before commit. Legacy schedules without a deadline require an
explicit migration/backfill policy before promotion.

Already-scheduled campaigns may still promote into durable QUEUED with the
original scheduled_for-based deadline during a migration hold. Admission records a
visible bounded preparation hold until dispatch/coverage is ready or deadline
expires (ADR-01/06). No fabricated HTTP 409 applies to the scheduler and no
unbounded deadline extension is allowed. New scheduling during prolonged holds
follows the explicit admission/load policy set in SPEC.

After commit return 202 with id, QUEUED snapshot, queued_at and message.
Kafka outage does not revert DRAFT or delete the key. It is not an API failure
after acceptance. HTTP transaction failures use generic internal server error;
existing mapped domain 4xx remain explicit. No state transition implies a
device received anything.

Invariant: every successful new QUEUED acceptance writes exactly one outbox
record in the same transaction. The record may later be removed after publish
ack; **it is not required to remain while the notification is QUEUED**.
UNIQUE(notification_id) is sufficient while resubmission is out of v1 scope;
future repeated-send semantics require an explicit send-generation identity.

### Alternatives rejected

Key reservation in a previously committed transaction can return false-success
replays. Compensating rollback after publication cannot undo external messages.
Kafka transactions do not atomically include the PostgreSQL status/key writes.

### Consequences

Remove the current publish-failure rollback and obsolete query if no other
caller remains. Update the send API's 202 meaning when implemented. Scheduled
and immediate acceptance set the same deadline origin; worker does not reset it.

## A-2. Scheduler-owned dispatcher; one row per bounded transaction

### Context

The server eager publisher raced the old scheduler drain. A transaction that
locks 100 rows across sequential broker I/O is not reliably short during outage.

### Decision

Scheduler owns the dispatcher, independent from due-notification promotion.
Run at startup and on proposed OUTBOX_POLL_INTERVAL=1s. The API server does
not publish eagerly; durable acceptance is its only success boundary. An
optional wake signal is a latency optimization, never the recovery guarantee.

For each row, under a bounded context (proposed 5s total DB+broker operation):
BEGIN; SELECT one due row ordered by next_publish_at,id FOR UPDATE SKIP LOCKED;
publish the existing NotificationSendRequested with persisted notification ID,
requested_at and scheduled_for; delete that exact row only after broker ack;
COMMIT. Do not lock an entire batch through network calls. No provider I/O.
Each tick has bounded row count/time budget, and each row has its own commit.

On a broker error, preserve the row, increment publish_attempts, store a bounded
last_error and persist next_publish_at with capped backoff/jitter before commit.
If the transaction itself fails, rollback leaves the row available; local
backoff prevents an immediate retry loop. Order by due time and ID plus SKIP
LOCKED allows other due campaigns to proceed. Repeated failure raises alerts;
no age-based purge of unpublished work. If the campaign expires while Kafka
is unavailable, ADR-01 background admission/recovery finalizes it; a later
event remains a terminal no-op. Coordinate cleanup without losing audit evidence.

On broker ack followed by crash/commit uncertainty the row may publish again.
This is at-least-once, absorbed by atomic fan-out and generation-guarded delivery.
Multiple dispatchers use disjoint locked rows. Keeping the row lock during
this one bounded publish prevents routine eager/reaper races; it cannot remove
ack-before-DB-commit duplicates.

### Alternatives rejected

API eager publication introduces a second claimant and response-path Kafka
latency. Unleased claim-then-publish outside a transaction routinely duplicates
work. A future leased dispatcher is possible but not required for this small
campaign rate; no fifth binary is needed now.

### Consequences

Healthy initial publication waits for poll scheduling/capacity, not a guaranteed
<=1s bound. Remove direct server Kafka dependency if unused elsewhere after
implementation audit. Validate the 5s timeout against actual broker settings;
do not leave a detached publisher running after rollback/cancellation. Network
I/O inside a transaction is an explicit bounded trade-off to load-test.

Use [ADR-07](adr-07-kafka-topology.md)'s explicit single-message flush config
and true hash routing; the current producer does not provide those guarantees.
The pinned kafka-go WriteMessages contract allows a cancelled call to publish
after returning. Bound underlying transport/retry lifetimes as well as the
caller context, keep message buffers immutable, and treat cancellation as
uncertain acceptance. A released DB lock cannot recall an old network request;
late duplicates remain part of the documented consumer contract.

## A-3. Schema, rollout and generated code

### Context

The current last migration is 000015_seed_roles. Migration numbers in prior
drafts were reservations, not existing files.

### Decision

NEW notification_send_outbox fields: id, notification_id (unique), requested_at,
scheduled_for, created_at, publish_attempts, next_publish_at and bounded
last_error. Add a due-order index. FK/delete policy must match the retained
notification lifecycle; no delete of accepted campaign while work is pending.
The idempotency schema belongs to ADR-01 A-8 and is implemented in the same
acceptance milestone. Queued time/deadline columns must exist before either
server/scheduler starts using them.

NEW db/queries/notification_send_outbox.sql and repository/send_outbox.go wrap
transaction-scoped insert, claim-one, delete, defer and pending-count queries.
Use sqlc.New(tx)/WithTx as appropriate; never accidentally call pool-bound
queries from inside the acceptance transaction. Add the necessary Transactor
dependency to notification service/module and scheduler.

Rollout includes an inventory of legacy QUEUED/PROCESSING rows and existing
deliveries. Backfill publication only for reviewed eligible QUEUED rows using
a recorded reconstructed acceptance time; do not backfill partial PROCESSING
as safe fresh sends. Pause/drain old publishers/consumers and deploy the new
admission/lease behavior before enabling new dispatch. Preserve evidence of
ambiguous old sends and handle them explicitly. Older binaries must not
continue bypassing the new acceptance invariant during rolling deployment.

Companion SPEC fixes actual migration numbers/order, up/down pairs, backfill
eligibility, indexes and transaction queries. Migrations precede make sqlc;
commit generated code with SQL. Do not hand-edit generated code. Regenerate
mocks only if a consumed mocked interface actually changes.

Down migration is not an operational rollback plan: never drop pending outbox,
keys or lease/intent state to deploy old code. Prefer compatible schema with
feature disable/drain or roll-forward, verified in staging.

### Alternatives rejected

Blind backfill of every old PROCESSING row can duplicate historical pushes.
Dropping pending tables destroys the guarantee being introduced. Prescribing
fixed migration numbers before milestone ordering is unnecessary coupling.

### Consequences

Implementation must update docs/api/notifications-api.md, docs/database.md,
Godoc and verified code-review findings. No runtime bug is fixed by this ADR.

## A-4. Configuration and observability

### Context

Publication cadence, campaign promotion cadence and retry deadline are separate.

### Decision

Scheduler-owned OUTBOX_POLL_INTERVAL=1s (proposed) is separate from the existing
SCHEDULER_INTERVAL. Configure bounded publish/transaction/tick budgets and
retry backoff in SPEC, validated at startup. PUSH_TTL (proposed 1h) is loaded
by both server and scheduler; each persists the same acceptance-based deadline.
The server no longer needs Kafka settings solely for Send; audit other uses
before removing any config requirement or producer lifecycle.

Track pending depth, oldest pending age, attempt/error rate, dispatch duration,
deadline-expired campaigns and admission lag, not just table row count. Outbox
empty means no pending publication, not "no queued or processing work".
Never log payload secrets/device tokens. ADR-08 owns alerts and readiness.

### Alternatives rejected

A single timer setting couples campaign timing to Kafka retry. Purging failed
rows to keep the table empty hides lost work. Claiming poll interval is the
worst-case completion time ignores backlog, backoff and downtime.

### Consequences

Numeric defaults remain proposed; staging evidence and accepted SLO set final
values. All deployment/config docs are updated when implementation lands.

## A-5. Duplicate and ordering contract

### Context

A transactional outbox closes a missing-event window, not external exactly-once.

### Decision

Publication ack may precede DB delete commit, so duplicate events are expected.
The deterministic notification_<id> key stays unchanged, but same partition
does not serialize DB recovery or provider tasks. ADR-01's complete fan-out,
device budget and generation lease handle replay; the previous PROCESSING row
count guard is not accepted as a safety prerequisite.

At-least-once publication does not restore historical replay order and cannot
prove exactly-once FCM/device effects. Event IDs/versioning and downstream
update ordering belong in the event-contract review, especially ADR-05.

### Alternatives rejected

The former claim that duplicate exposure is unchanged/fully closed by a CAS
overstates safety. At-least-once publication plus database fences still leaves
provider-accept-before-result-write and stale in-flight request uncertainty.

### Consequences

Enable production dispatch only with the revised admission/recovery/lease
milestone and its failure tests. Do not treat independent commit order as a
reason to expose incomplete guards to live traffic.

## Design verification cases for the companion SPEC

| Input / failure | Condition | Expected observable result |
| --- | --- | --- |
| Concurrent same-key sends | One acceptance tx pending | No replay 202 before commit; rollback lets next request acquire |
| Claim succeeds, insert fails | Inside acceptance tx | Status/key/outbox all roll back |
| Broker down | Acceptance tx committed | 202, durable key/outbox/deadline; no reverse state transition |
| API response lost | Commit outcome uncertain to caller | Same-key retry resolves committed result or safely claims |
| Two scheduler replicas | Same scheduled campaign | One accepted transition/outbox record |
| Two dispatchers | Same pending row | One locked claimant; no routine duplicate publication |
| Broker ack then crash | Delete transaction uncommitted | Row republishes; atomic admission remains no-op on replay |
| Slow/dead broker | Row timeout elapsed | Transaction/connection released; row retained with retry/backoff |
| One repeatedly failing row | Other due rows exist | Bounded tick plus deferral avoids starvation |
| Legacy rollout/backfill | Ambiguous historical delivery | Inventory/reconciliation required; no automatic resend assumption |
| Rollback requested | Pending records exist | Preserve compatible schema/state; tested drain/roll-forward plan |
