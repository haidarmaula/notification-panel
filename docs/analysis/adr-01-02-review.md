# Initial review of ADR-01 and ADR-02

Date: 2026-11-02. Status: initial review and design revision follow-up;
not yet accepted for implementation.
Scope: relationships between the retry ledger, fan-out, attempt ownership, Kafka consumption,
offsets, recovery, and API handoff. No runtime changes are part of this review.

Documents reviewed: [ADR-01](../adr/adr-01-provider-retry.md),
[ADR-02](../adr/adr-02-worker-consumption.md), and the relationships stated
by [ADR-03](../adr/adr-03-transactional-outbox.md). The ADR-03 review/revision
and [follow-up ADR-04/05 review](adr-04-05-review.md) are available at
the design level; cross-ADR acceptance remains step 15. The latest requirements are in the
[production plan](production-readiness-plan.md).

## Conclusion

Separating persistent provider retry from consumer retry is a sound
direction. D1-D7 below record findings against the **pre-revision** version on
2026-11-02; old quotations do not describe behavior still recommended by the latest ADRs.
ADR-01/02/03 have been revised to address them at the design level. Their
proposed status remains; there has been no runtime verification or production acceptance.

## Revision follow-up, 2026-11-02

| Finding | Latest design decision | Evidence still required |
| --- | --- | --- |
| D1 | ADR-02 A-1/A-3: one in-flight admission, stop fetching when DLQ/commit fails; separate provider pool | Integration tests for offsets, rebalance, commit failure, and shutdown |
| D2 | ADR-01 A-10: campaign lease with owner/generation/expiry; per-delivery claim budget | Lease expiry, takeover, and stale-result tests; residual external duplicates remain possible |
| D3 | ADR-01 A-6/A-11: scan expired leases/budgets/TTL, receipt states, and QUEUED before admission | Clock/fault tests for final claim, queue expiry, and pre-admission quarantine |
| D4 | ADR-01 A-10: atomic audience snapshot + completion marker, rather than a row-count guard | Transaction rollback/snapshot benchmark for 20,000 devices and legacy/FK policy |
| D5 | ADR-01 A-8 + ADR-03 A-1: key, claim, deadline, and outbox in one transaction | Concurrent-key, rollback, unknown-commit, and authorization tests |
| D6 | ADR-01 A-3/A-4/A-9 + ADR-02 A-1/A-5: device-based sizing, provider retry floors, operation deadlines | Quota/SDK audit, benchmark, and final configuration |
| D7 | ADR-02 A-2/A-4: source-position dedup, replay as a new event, and resolution audit | Replay-after-ack-crash and concurrent-operator tests |

Additional changes: ADR-03 removes the server's eager publisher, uses one
row per timeout-bounded publish transaction, and does not promise the poll
interval as an absolute time bound. The retry budget shifts from campaigns to
devices so a crash during a broadcast does not consume the allowance of devices
never submitted. Following the cross-ADR review, immediate TTL uses
durable acceptance, while scheduled TTL uses scheduled_for; the worker reads
the persisted deadline. See [CR1–CR9](adr-01-11-consistency-review.md)
for the latest protocol; decisions must enter the SPEC before the related implementation.

## D1 — Blocker: messages without a dead letter can be skipped by the next commit

**Evidence:** ADR-02 A-2 states that a failed dead-letter insert -> do not commit,
but continue to the next message. A-3 assumes that per-message commits prevent
previous messages from being committed too. In the repository's dependency,
`github.com/segmentio/kafka-go@v0.4.51/reader.go`, the `Reader.CommitMessages` Godoc
states that committing the highest offset also commits all previous offsets
in that partition. This behavior applies even when each call contains only
one message.

**Impact:** a database error can remove an event from the Kafka recovery path
even though its dead letter has not been stored. The ADR-01 A-9 pool adds the risk of out-of-order
completion: a higher offset finishes first.

**Proposed revision:** define a contiguous safe commit watermark per
partition. Offsets may advance only past messages completed durably.
If there is a gap, hold commits from passing it and bound intake/in-flight work; choose
an explicit pause/stop/restart strategy or coordinator. Define the rebalance
and shutdown lifecycle. Do not rely on per-call commits for isolation.

**Required case:** offset 10 fails processing and dead-lettering, while offset 11
succeeds; offset 10 remains available after restart. Repeat when the pool
completes 11 before 10 and when the partition assignment changes.

## D2 — Blocker: an atomic increment is not yet an exclusive attempt claim

**Evidence:** ADR-01 A-10 proposes an UPDATE incrementing `attempt_count` with
predicates for PROCESSING status, due time, budget, and deadline. It does not specify
a predicate change marking the attempt as owned by a worker. A-9 also
assumes a deterministic Kafka key preserves ordering when using a shared pool.

**Impact:** two drivers can execute successive UPDATEs and both
succeed while `next_attempt_at` remains NULL/due and the budget is not exhausted.
Each sends the same PENDING set. A partition key does not serialize
pool execution or recovery scans on another replica.

**Proposed revision:** a durable claim/lease with an owner or fencing token,
expiry, and finalization guard. The claim changes eligibility until released/expired.
Define the provider timeout versus the lease, renewal if needed, and handling of
old-worker results after lease loss. A local mutex alone is insufficient across replicas.
Distinguish rejection of stale DB writes from external duplicate pushes: DB fencing
alone does not cancel a request already accepted by the provider.

**Required case:** simultaneous timer, recovery scan, and Kafka replay yield only
one active owner. Test a worker paused for a long time, lease expiry, takeover, and an
old result arriving after takeover. Document the residual duplicate window.

## D3 — Blocker: the recovery scan does not cover all states requiring finalization

**Evidence:** ADR-01 A-1 selects zero-row cases, due retries with a still-valid
budget/deadline, or all-terminal rows. There is no explicit branch for PENDING with
an expired deadline / exhausted budget. A-10 calls finalize only after entry.

**Impact:** a crash after claiming the last attempt or while awaiting retry
can leave PROCESSING+PENDING forever; this does not meet the
“not stranded” claim. An active lease must also prevent the scan from taking work
still owned by a worker.

**Proposed revision:** the scan covers expired leases, expired TTL, exhausted
budgets, and all-terminal states (including DELIVERED/OPENED after ADR-05).
Choose finalize/retry explicitly and idempotently. Define the reference timestamp,
the state before deadline creation, and still-active leases. Scan batching/pagination
and fairness must bound load and avoid starvation.

**Required case:** restart after the last attempt is claimed but before
finalization; the deadline passes while the worker is down; SENT changes to OPENED
before campaign finalization. All reach a final state without an operator.

## D4 — Blocker: some PENDING rows do not prove complete fan-out

**Evidence:** ADR-01 A-10 says to upsert PENDING before the provider, and PENDING
entries mean the outstanding set. A-1 permits a commit once PENDING is
stored. There is no transaction boundary or fan-out-complete marker
distinguishing partial inserts from the entire snapshotted audience.

**Impact:** a crash during audience materialization can cause recovery to
send to some devices and then consider the campaign complete. Audience/token
changes after the crash can also change retry targets.

**Proposed revision:** choose an atomic snapshot/fan-out transaction at a
proven scale, or paginated durable fan-out with a cursor and completion
marker. Do not allow submission/finalization/ack before the chosen
durability condition is met. Define snapshot timing, eligible devices,
token ownership changes, and device deletion during retry.

**Required case:** stop the process after some intents are written; restart
still produces the entire original audience, without duplicate rows or a
prematurely COMPLETED campaign. Test token refresh/account switch during retry.

## D5 — Blocker: an idempotency result can be visible before work is accepted

**Evidence:** ADR-01 A-8 writes key+snapshot before the claim and deletes the key when
publishing fails. The ADR-03 relationship section removes publish rollback, but
still places the key upsert before the claim+outbox transaction.

**Impact:** a second request can receive a replayed 202 for an already-visible
key even though the first request's claim/outbox fails or has not committed. The
“202 = durably accepted” contract is not met. The old and new rules also differ.

**Proposed revision:** key reservation/result, DRAFT->QUEUED, and outbox
must have transaction/serialization guarantees that prevent replay from returning
success before durable acceptance. Choose a single transaction or an explicit key
lifecycle with handling for in-progress requests. Align ADR-01 A-8
with ADR-03 and replay authorization; do not merely leave a reconciliation
note in another document.

**Required case:** a second request reads the key before the first request
commits; rollback/crash of the first request leaves no false successful replay.

## D6 — Should-fix: performance and budget assumptions do not match the latest requirements

**Evidence:** ADR-01 A-9 uses 100 campaigns/second and a burst of 1,000/second for
pool=32. The user confirmed 20,000 active devices and about 5 campaigns
per month. ADR-02 A-1 states a stall of about 15 seconds based on backoff,
but total invocation time and DB unavailability for dead-lettering do not yet
have complete bounds. The “no herd” rationale ignores multiple replicas.

**Proposed revision:** size by device submissions/second,
batch latency, DB cost, provider quota, and recovery load. Distinguish concurrency
from rate limits. Define per-operation deadlines and a total retry budget;
consider jitter for shared errors across replicas. Do not claim
15 seconds as a total bound or worker count as a quota limiter.

**Required case:** a full broadcast, two overlapping campaigns, a retry storm, and
provider slowdown still maintain memory/concurrency/rate bounds.

## D7 — Should-fix: a deterministic key is not a replay ordering contract

**Evidence:** ADR-02 A-2 says requeue with the original key preserves order;
the `PublishSendRequested` producer rebuilds the key from the notification ID.
Requeue creates a new record at the end of the log; it does not restore the original position.
Partition mapping also depends on an unchanged topology/partitioner.

**Proposed revision:** state that requeue is a new replay event with
DB dedup; do not promise historical order. Define validation of the decoded
event against the notification ID/key, dead-letter dedup by source
topic/partition/offset, and handling of a crash after DLQ insert but before commit.
Preserve replay/resolution information for audit under the retention policy.
Do not assume APNs/FCM duplicate prevention without a verified provider
contract.

**Required case:** replay after a terminal campaign is a no-op; successful DLQ insert
followed by a crash before commit does not produce unbounded duplicate quarantine entries.

## Review limits and next steps

- This reviews a proposed design; it is not a concurrency/runtime test result.
- Other external interface/provider claims have not been fully audited.
- D1-D7 have design responses in the follow-up table; no
  runtime finding is closed merely because documents were revised.
- ADR-01/02/03 need another check together with ADR-04/05, especially the token schema,
  lease guards on delivery updates, event identity/ordering, and outbox draining.
- Design case matrices are available at the end of ADR-01/02/03; their SPEC
  counterparts must still define the exact queries, files, migrations, configuration, and tests.
