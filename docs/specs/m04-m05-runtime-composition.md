# M04/M05 runtime composition

Date: 2026-11-05. Status: authenticated admission, source-bound publication,
scheduler composition and protected per-process worker/scheduler entry points are
implemented and locally tested. The admission worker runs `admission_only`. M06 adds a dedicated
[`delivery_only` worker](../runbooks/delivery-worker.md). No production source/application is
enabled. See [local verification](../analysis/runtime-composition-verification.md).

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

## Executable policy and lifecycle

`cmd/worker` and `cmd/scheduler` compile in their process role and accept only
`--help` (`-h`). They load `/etc/hello/worker.json` or
`/etc/hello/scheduler.json`; Darwin uses `/private/etc/hello/`. The shared
`internal/deployment` checks retain the replay command's protected root-owned
ancestry, file modes, symlink/extended-ACL rejection, exact bounded JSON, verified
DB TLS, and authenticated broker transport. No argv/environment path, transport,
role or trust override exists. Replay issuer/key/grant fields are rejected by
daemon schemas. Configuration remains private and immutable after one load.

Common required fields are `database_url`, `log_source_id`, `cluster_identity`,
`topic_name`, `topic_incarnation`, `partition_count`, `kafka`, `retry_min_ms` and
`retry_max_ms`. The broker object has the same exact fields as the
[replay policy](m05-replay-command.md). Retry minimum is at least 100ms and maximum
at most 300,000ms, with maximum no smaller than minimum. Worker additionally
requires `mode: "admission_only"` and `group_id` for Kafka admission, or
`mode: "delivery_only"` and an application-scoped delivery policy for FCM execution.
Scheduler additionally requires `poll_interval_ms` (100–60,000) and `page_size`
(1–100). These bounds are implementation limits, not production tuning approval.

Startup has a 15-second parent budget. Each daemon opens one two-connection pool,
pings verified DB TLS, checks unsealed runtime/active source registration and live
broker identity/topic/partition metadata. The admission worker preflights metadata without a
Produce request, closes that temporary transport and starts its supervised group.
Scheduler retains its producer and rechecks metadata before publication. Startup
preflight does not replace transaction-time authority/epoch/source gates.

SIGINT/SIGTERM cancel and join work, drain the producer, then close the pool.
Owned stdout/stderr close on cancellation to interrupt blocked pipe writes; the
interruption callback is joined. Clean cancellation exits 0; runtime, startup or
drain failures exit 1; invalid arguments exit 2. Logs contain static failure
messages and no raw dependency errors/policy values. Shutdown must be supervised:
SDK-owned I/O may outlive call cancellation until producer Close finishes.

The dedicated M06 worker polls the delivery ledger and publishes its updates. Real issuer integration, source
incarnation/ACL review, HA, restore/backpressure, capacity and production acceptance
remain release gates. There is no automatic source/application activation.
