# M04/M05 runtime composition

Date: 2026-11-05. Status: authenticated consumer, source-bound publisher and
scheduler composition are implemented and tested as libraries. The worker and
scheduler executable entry points remain intentionally unwired pending trusted
per-process configuration and startup/shutdown composition. No provider is called
and no production source/application is enabled. See
[local verification](../analysis/runtime-composition-verification.md).

## Authenticated admission

`NewAuthenticatedKafkaAdmission` requires verified TLS 1.2+, mTLS or SASL identity,
an explicit reviewed source/partition count and a bounded group ID. Consumer-group
coordinator connections, metadata requests and partition readers all use the same
retained policy. Broker addresses/TLS/root pools are copied; certificate/key and
SASL implementations must remain immutable. Do not log this configuration.

Source startup/assignment verification compares live cluster/topic/contiguous
partition metadata with the manifest before fetchers start. Current DB approval,
acceptance, epochs and complete preparation remain independently checked by the
existing adapter/Preparer. Source incarnation is operator-approved; SDK metadata
still cannot prove its UUID. `NewKafkaAdmission` retains plaintext fixture
compatibility and must not be selected by deployed process composition.

The real mTLS handoff fixture exercises two partitions/members, generation
revocation during blocked preparation, no premature offset commit, durable poison
evidence, redelivery without duplicate intents and joined fetchers. It uses the
same assertions as the existing plaintext fixture.

## Source-bound outbox

`NewSourceOutbox` retains an immutable `SourceManifest`. Each transaction locks
runtime -> physical source -> one outbox and holds approval through the broker
call/result transaction. Inactive, missing or mismatched registration returns held
before claiming a row or advancing publish attempts. Database errors still fail.
The publisher must separately compare live broker metadata and own its drain.

`NewOutbox` retains source-unbound fixture compatibility. Neither syntax validation
nor a configured manifest activates or creates a source. Source/epoch transitions
still require supervised drain: a call timeout does not fence producer-owned I/O.

## Bounded due-schedule polling

`PromoteDueSchedules` accepts 1–100 candidates and a 30-second sweep budget.
Discovery has at most five seconds, takes no child locks, and returns application
public UUIDs plus campaign IDs. It selects only unsealed current origin/source
lineage, preventing old-epoch rows from filling the first page forever.

Expired accepted schedules remain eligible for terminalization. Migration holds
are not a discovery filter: existing accepted schedules may become QUEUED while
preparation/provider eligibility remains held. Each candidate calls the existing
five-second `PromoteSchedule` transaction and rechecks runtime -> app -> campaign.
No deadline/epoch is restamped. Counts retain acknowledged changes when a later
candidate fails; the sweep stops rather than replaying its already committed
prefix. Stale/restored work stays intact for reviewed reconciliation.

## Scheduler and process-loop ownership

`internal/scheduler.Runtime` constructs promotion, source-bound outbox, QUEUED
recovery and expired replay-claim cleanup from one pool/query set. It starts one
nonoverlapping polling loop for each responsibility. Sweeps have 30-second parent
budgets and 1–100 candidate/publication limits. Callbacks preserve partial progress
and emit aggregate counts without per-user data. Pool/publisher remain caller-owned.

`internal/runloop` provides:

- `RunTask`: immediate polling, bounded callback contexts, success resetting the
  failure streak, and capped jittered delay after failures.
- `Supervise`: restart a whole stopped component only after its resource cleanup
  returns. It does not retry an I/O transaction or recreate execution proof.
- `RunGroup`: validate callbacks, cancel siblings on a stop/failure, and join every
  callback before returning. There are no detached timeout/cleanup goroutines.

Retry minima are at least 10ms and maxima at most one hour. Names are bounded
static identifiers. Dependency error text is never logged by these loops because
it can contain credentials/payloads. Callbacks must honor cancellation; a context
cannot forcibly interrupt an uncooperative function. These are implementation
limits, not approved production polling/SLO values.

The actual composed fixture runs scheduling -> source-bound outbox -> Kafka mTLS
-> authenticated admission with concurrent recovery/cleanup. It observes one
snapshot/untouched delivery intent, an empty outbox, one safe committed Kafka
position and preserved deadline before canceling/joining the runtime. This does
not prove FCM execution, production HA, real identity integration or capacity.

## Next executable boundary

Share the tested protected-policy/database/broker parsing primitives with daemon
configuration, without requiring operator replay keys/grants in worker/scheduler.
Then compose `cmd/worker` and `cmd/scheduler` with one pool, immutable per-process
policy, authenticated source checks, signal cancellation, joined tasks and
producer drain before pool close. Exercise dependency/startup/shutdown failures
through those executable paths. Provider execution remains M06; real issuer,
incarnation/ACL, restore/backpressure and production acceptance remain gates.
