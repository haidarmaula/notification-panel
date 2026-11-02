# ADR-01 — Durable fan-out, provider retry and delivery ownership

**Status:** proposed, revised 2026-11-02 after the
[ADR-01/02 review](../analysis/adr-01-02-review.md). Not implemented or accepted.
Supersedes `bigpickle-retry.md` and the previous version of this proposal.
The old SPEC is historical input, not an implementation contract.

**Scope:** notifications processor/provider/service, worker, repositories,
SQL queries/migrations/generated sqlc, configuration and API/database docs.
NEW companion SPEC: `docs/specs/adr-01-provider-retry.md` (not yet written).


Cross-ADR protocol: [consistency review](../analysis/adr-01-11-consistency-review.md).
Its shared lock/identity rules and verification gates apply to this proposal;
review completion does not mean acceptance or runtime implementation.

## Requirements and dependencies

Confirmed: approximately 20,000 active devices, five campaigns/month, full
active-user broadcast, FCM for Android and iOS (APNs through Firebase), and
retirement of OneSignal. See [production plan](../analysis/production-readiness-plan.md).
There is no accepted 100 campaign/s requirement or numeric broadcast SLA.
Defaults below are design proposals, to calibrate in staging.

- ADR-02 owns serial Kafka admission, dead-letter and safe offset commits.
- ADR-03 owns durable QUEUED acceptance, including the atomic idempotency
  transaction in A-8. These changes must be specified together.
- ADR-04 owns FCM construction, supported SDK send methods, credential,
  immutable registration identity and owner_epoch. Its revised proposal uses
  explicit FCM selection, not implicit OneSignal defaults.
- ADR-05 owns authenticated per-device receipt and the versioned update outbox.
  Valid early receipt may promote PENDING to DELIVERED/OPENED under the
  campaign lock without a provider-worker lease; worker writes may never
  regress that observed state. Late FAILED observations remain separate facts.
- [ADR-06](adr-06-onesignal-fcm-cutover.md) owns migration eligibility, dispatch
  holds, coverage, React Native adoption and cutover. There is no
  assumed OneSignal-player-ID to FCM-token conversion.

## Background and evidence

The working-tree `Processor.ProcessSendRequested` sends before recording
deliveries; `alreadyProcessed` skips PROCESSING with any delivery rows. This
prevents neither crash-window duplicates nor loss of unrecorded results.
`cmd/worker/main.go` currently combines consumption and provider submission.
`000012_create_notification_deliveries.up.sql` has PENDING and retry_count,
but no per-device uniqueness or durable attempt ownership. This ADR proposes
new behavior; none of these findings is closed in runtime by this document.

## A-1. Separate Kafka admission from provider delivery

### Context

An entire broadcast must not occupy the Kafka commit path or make offsets
depend on out-of-order provider work. Durable intent must outlive the process.

### Decision

- Kafka admission invokes NEW `PrepareSendRequested`, which performs A-10's
  complete atomic fan-out and no provider I/O. The current combined processor
  must be split; naming and exact signatures are fixed in the companion SPEC.
- Admission returns nil only after fan-out_complete_at and all intents commit,
  or after recognizing a durable terminal notification. PROCESSING with some
  rows is not sufficient. Error/panic before commit rolls the transaction back.
- Provider drivers discover committed intents from PostgreSQL. Local wakeups
  and a bounded ready queue improve latency; the recovery scan is authoritative
  if a wakeup is lost. Kafka commits use ADR-02, not provider completion.
- Attempt budget is **per delivery**, default five claimed submissions per
  device. A claim consumes budget even if a crash prevents external I/O; actual
  network receipt cannot be known transactionally. SDK-internal retries must
  be inspected and bounded/observed separately; do not equate a claimed
  submission with one physical HTTP request.
- Per-device next_attempt_at and last_attempt_at persist. Campaign-level
  attempt_count, if exposed, is a separately defined diagnostic, never the
  per-device retry budget. Do not reset any budget on restart/requeue.

### Alternatives rejected

Provider calls before intent commit leave no resumable ledger. A parallel
Kafka processing pool needs a commit coordinator for little benefit at five
campaigns/month. Campaign-wide retry counts unfairly charge unattempted devices
when a worker crashes halfway through a broadcast.

### Consequences

The previous campaign-wide five-round design and parallel Kafka ready queue
are replaced. Fast broadcasts depend on bounded device submissions, not
campaign ingestion throughput. This changes the old draft's test expectations.

## A-2. Provider error classification

### Context

The current providers return errors that cannot reliably drive retry policy.
HTTP status alone does not prove a particular token is invalid.

### Decision

Introduce typed transient/permanent errors, explicit reason codes, optional
RetryAfter and per-token results. Results are one per input identity, including
on batch-level errors. Missing/duplicate/unattributable results become explicit
unconfirmed transient outcomes, never device_token_id=0 or silent success.

For FCM, distinguish unregistered/confirmed-invalid token, payload error,
sender/project mismatch, auth/APNs credential error, quota and availability.
INVALID_ARGUMENT alone must not deactivate tokens: validate the payload and
provider error details first. Batch auth/payload failures affect the submitted
batch; do not disable unrelated devices. Unknown external outcomes remain
transient within budget/TTL. Error codes/reasons are persisted and bounded;
never put tokens/credentials in logs or API error messages.

### Alternatives rejected

String matching in the processor and deactivating every token on 400/404 can
disable valid registrations. Retrying every permanent error wastes quota.

### Consequences

ADR-04/SPEC must verify current Firebase SDK error helpers and retry behavior.
The stale provider interface Godoc must be replaced during implementation.

## A-3. Bounded provider batches and rate control

### Context

The current FCM provider chunks at 500 in `fcm_provider.go`; batch size is not
the same as HTTP concurrency, rate quota or campaign size.

### Decision

Page eligible PENDING candidates, initially 100; this is a DB page limit, not
one charged attempt or a concurrent SDK batch. ADR-04's bounded executor uses
one logical Send per device. Rate-limit device submissions and bound underlying
SDK concurrency/retries, not only outer driver calls.
No unbounded goroutine-per-device and no one-shot million-row Go allocation.
OneSignal improvements are transitional only if ADR-06 requires its continued
operation; they are not a prerequisite for FCM-only v1.

### Alternatives rejected

Treating one SDK batch as one quota request can substantially undercount load.
Porting all OneSignal feature work before FCM migration expands scope without
serving the confirmed end-state.

### Consequences

Benchmark device submissions/s with realistic DB and SDK behavior. The API must
not promise a <=60-second broadcast until measured and accepted.

## A-4. Schedule, deadline and attempt budget

### Context

Retry timing must survive restarts and respect provider advice and freshness.

### Decision

- Proposed campaign deadline: immediate sends use durable QUEUED acceptance
  time + PUSH_TTL (default 1h). Scheduled sends use scheduled_for + PUSH_TTL,
  not actual late promotion. Queue wait/outage/holds never grant a fresh window.
  Set the schedule deadline when the schedule is committed; only an authorized
  pre-acceptance schedule edit may recompute it. ADR-03 owns atomic persistence.
  TTL value remains a proposal requiring product review.
- With per-device claim count i, exponential window is
  min(max_delay, base_delay * 2^(i-1)); proposed base 10s, cap 5m. Use bounded
  jitter plus provider floors: ordinary failed FCM requests wait at least 10s;
  quota 429 waits at least 60s; RetryAfter is a lower bound, never shortened.
  Store the final eligible time; SDK backoff must not defeat the outer policy.
- If the next allowed time is at/after deadline, finalize pending work instead
  of making a late call. A valid active lease may persist results from a call
  started before deadline, but must not start new calls after it.
- Attempts increase only for devices reserved for submission under A-10.
  Breaker/rate-limit deferral does not consume attempts. Internal DB errors
  do not increment them, except uncertainty after a committed submission claim.

### Alternatives rejected

The previous full-jitter range starting at zero could violate provider minimum
waits. Deadline from first attempt can retain stale campaigns through an
unbounded queue wait. Neither message tags nor collapse keys are assumed to
provide external exactly-once semantics.

### Consequences

TTL origin and initial defaults are revised design decisions, still proposed.
Use a controlled clock for scheduling tests and DB time for leases/deadlines.
Provider references, checked 2026-11-02:
[FCM errors](https://firebase.google.com/docs/cloud-messaging/error-codes),
[FCM scaling](https://firebase.google.com/docs/cloud-messaging/scale-fcm).

## A-5. Token validity and ownership

### Context

Tokens may refresh or transfer owners while a campaign is waiting. Existing
device/user foreign keys can cascade-delete delivery history.

### Decision

Freeze token/user/provider identity in each intent at fan-out. Before sending,
verify that the registration is still active, same owner and same token
generation; mismatch becomes terminal FAILED with an explicit token_changed
or device_inactive reason. Do not expand the audience again on retry. Token
deactivation is a conditional write matching the original owner/token version,
so an old response cannot disable a newly registered token.

Hard deletion/cascade must not silently erase outstanding intent. ADR-04/09/10
must define soft deletion or retained delivery identities and the FK migration
before enabling delivery. Store token snapshots as sensitive data with bounded
retention/access; do not add them to public DTOs. Validation immediately before
submission cannot revoke a request already accepted by the provider; logout
privacy must also be enforced by the mobile payload/display contract.

### Alternatives rejected

Reading the latest token by device ID alone can send an old user's campaign
to a new owner. Blind deactivation can disable a refreshed registration.

### Consequences

Schema/device lifecycle work is an explicit cross-ADR dependency, not a claim
that current deletion behavior is safe. Test refresh, account switch and delete.

## A-6. Idempotent finalization

### Context

A delivery ledger must reach a queryable end state after deadline/budget
exhaustion, including crashes just before campaign finalization.

### Decision

Only a complete fan-out may become COMPLETED or use device-outcome aggregation.
Pre-fanout expiry may instead become FAILED/ttl_expired with snapshot counts
unknown, not fabricated zero; record durable lifecycle evidence without a fake
human actor or per-device event. Successful submission states are SENT,
DELIVERED and OPENED; FAILED is terminal unsuccessful. If PENDING remains,
the campaign stays PROCESSING unless deadline/budget/permanent outcomes retire
that work. Budget is evaluated per device, not campaign diagnostic count.

Finalize expired/exhausted PENDING rows with stable reason codes such as
ttl_expired, retry_budget_exhausted, provider_auth_failed,
provider_payload_rejected, token_invalid, token_changed, or device_inactive.
If any device succeeded, the campaign becomes COMPLETED (possibly partial
success; statistics expose failures). With a nonempty audience and no success,
it becomes FAILED with a stable campaign reason. Complete zero-device fan-out
becomes COMPLETED. Do not confuse a partial insert set with zero audience.

All worker result writes/finalization are lease-generation guarded. A late
worker cannot regress receipt status or overwrite a newer generation. Final
status and any delivery updates/outbox records (ADR-05) commit together.

### Alternatives rejected

Leaving exhausted rows PENDING strands work. Marking mixed success as total
failure hides what already reached the provider and encourages duplicate sends.

### Consequences

FAILED is terminal; operator requeue does not reset budgets or revive it.
An explicit product resubmit action remains outside v1 unless separately scoped.

## A-7. Provider breaker

### Context

A process-local breaker limits repeated traffic into provider outages.

### Decision

Use a concurrency-safe breaker per provider instance: proposed threshold 5
consecutive transient logical per-device submission failures, cooldown 30s,
one half-open device probe. Successful submission resets the streak; permanent
token rejection is neutral. SDK internal transport retries are not independent
breaker outcomes. Configuration/auth failures raise an alert, not a token purge.
An acquired permit may remain in flight when the breaker opens; bounded already
admitted work can finish. A failed DB reservation releases a probe neutrally.

Check breaker/rate limiter before reserving attempts. Open breaker persists
the next eligible time without consuming budget; expired work still finalizes.
Replica-local breakers are not a global rate limiter; the deployment must
allocate submission quota across replica count (A-9).

### Alternatives rejected

Treating invalid devices as provider outages blocks healthy traffic. Adding
an external coordinated breaker is unnecessary before measured need.

### Consequences

Restart begins closed; document aggregate traffic with multiple replicas.

## A-8. Send idempotency and acceptance transaction

### Context

A replay must not return success for an uncommitted or rolled-back send.

### Decision

- Authorize the current staff actor and notification access before returning
  any replay. Validate Idempotency-Key (1–255 chars, no controls); an absent or
  empty header uses a derived notification key scoped to that staff actor.
- NEW notification_send_keys is scoped by (staff_user_id, key_source, key),
  where key_source separates client and derived keys. Store notification ID,
  immutable QUEUED response snapshot, queued_at and expires_at (default 24h).
- **One PostgreSQL transaction** reserves/locks the key, conditionally claims
  DRAFT->QUEUED, sets deadline_at, inserts the send outbox and stores the result.
  The key is never committed before claim/outbox. After required authority locks,
  the relative order is key then notification (shared CR1 lock protocol).
  Cryptographic authentication and provider I/O are outside this transaction.
  ADR-09/11 require current service-level permission/version checks for mutation
  and a minimized audit record in the same transaction. Replay is not another
  send audit or a bypass of current authorization.
- Same key concurrency waits on database uniqueness/row locking under a bounded
  transaction context. If the first transaction rolls back, the next may become
  the owner. If it commits, the next sees a replay. Lock timeout returns a
  generic internal server error, never speculative 202; safe retry uses the key.
- Unexpired same-actor/source/key with same notification returns stored 202
  and Idempotency-Replayed: true. Different notification returns 409. Expiry is
  checked under lock on access, not dependent on opportunistic purge running.
  Reuse of an expired key still must pass the notification's DRAFT guard.
- Claim failure rolls the entire reservation back. After commit, broker failure
  cannot delete the key, revert to DRAFT, or turn acceptance into a 500. If the
  HTTP response is lost or commit outcome ambiguous, retry resolves from durable
  state; do not attempt an out-of-transaction compensating delete.
- Response: 202 with id, status=QUEUED, queued_at and message. This is durable
  acceptance, not evidence of Kafka publication or device receipt.

### Alternatives rejected

A key committed before claim/outbox permits false-success replay. A global key
namespace creates actor collisions and complicates response authorization.

### Consequences

ADR-03 uses exactly this transaction. Expired-key cleanup is storage maintenance,
not correctness. Scheduled promotion needs no HTTP key but uses the same
claim+deadline+outbox invariant. Actual migrations and API docs follow in SPEC.

## A-9. Bounded delivery workers

### Context

The workload is a small number of large fan-outs, not thousands of campaigns/s.

### Decision

Keep Kafka admission serial (ADR-02). A separate provider pool handles bounded
chunks from the durable ledger; proposed two campaign drivers, ready queue 32,
100 devices/chunk. One valid campaign lease at a time; chunks within that
campaign may use bounded SDK concurrency, never concurrent campaign owners.
Queue full means defer the wakeup: durable scans find work later, no lost intent.

ADR-06's durable global dispatch flag, installation eligibility and route epoch
also gate reservation/submission. Missing or unreadable migration control fails
closed; a hold does not consume new attempt budget. Revalidate any explicitly
resumed pending execution without changing its audience/binding, budget or
deadline. A hold cannot recall in-flight calls or prevent normal TTL expiry.

Configure an explicit per-process device-submission rate and SDK concurrency
budget; replica allocations must sum within the project/provider budget. No
unrestricted autoscaling or assumption that batch=one request. Before v1 SPEC,
calibrate conservative defaults against quota and realistic latency; startup
must reject invalid/missing required rate configuration. Maintain fair queueing
across campaigns rather than retaining one lease until an entire outage ends.

### Alternatives rejected

The previous pool=32 campaign formula is unrelated to confirmed workload.
Parallel Kafka admission complicates offset safety without accelerating FCM
submission of one campaign.

### Consequences

Bound memory by page/chunk size plus configured workers and SDK concurrency.
Benchmark actual per-device throughput; no performance claim follows from knobs.

## A-10. Atomic audience snapshot and leased chunk submission

### Context

Incrementing an attempt counter alone is not an exclusive claim, and a partial
intent set is not a complete audience.

### Decision

**Preparation transaction:** use bounded PostgreSQL REPEATABLE READ; lock
notification and recheck terminal/completion state. Use the same transaction
snapshot and canonical audience predicate for coverage, counts and set-wise
intent inserts. Retry the whole transaction on serialization failure (40001)
or deadlock (40P01), within admission limits; never reuse an aborted snapshot.
For an eligible QUEUED notification insert active-device intents deduplicated by
(notification_id, device_token_id). Set fanout_complete_at and PROCESSING in the
same transaction. No network calls. Rollback means no new visible partial set;
retry resolves a new snapshot only if no prior complete snapshot committed.
Persist ADR-05's resolved audience/eligible-user/eligible-device counts in this
same transaction; do not reconstruct denominators from the live user table.
Each immutable intent also receives an opaque delivery ID, delivery_version=0
and receipt capability scope from ADR-05. Do not expose an ID/capability or
submit its payload before the complete intent transaction commits.
Empty audience also records completion. Reject unsupported/malformed targets
explicitly, not successful zero-delivery. Validate normal targets before queue
acceptance; legacy invalid events become quarantined with a visible reason.

Apply ADR-06's explicit pilot/full-broadcast coverage policy in this transaction.
If coverage became invalid after acceptance, commit only a preparation hold
reason/due time (no partial intent snapshot), return an admission error, and
let bounded admission/quarantine/recovery revisit readiness under the original
deadline. Never acknowledge an incomplete snapshot as successful admission.
Add persisted preparation_hold_reason and preparation_next_attempt_at in SPEC
so this deferral is visible, bounded and indexable rather than a hot loop.

Atomic preparation of 20,000 intents must pass a bounded transaction/load test.
If it cannot, replace with an explicit paginated fan-out cursor/completion ADR
revision; never silently truncate at current million-row fetch limits.
Pre-migration partial PROCESSING rows require an operator inventory and explicit
reconciliation; do not label them complete or automatically resend old pushes.

**Ownership:** NEW notification lease_owner, lease_generation and lease_until.
Atomic claim succeeds only if no valid lease, fan-out complete and actionable
work exists. Each acquisition increments generation; renew/result/release
require matching owner+generation and nonexpired lease using DB time. Proposed
lease 60s, renewal every 15s, provider request timeout 10s (at least the provider
recommended timeout); values must satisfy timeout < lease and renewal safety.
Before each request verify ownership. Failure to renew cancels local work.
Ownership checks and the resulting DB mutations share a short transaction
locking the campaign row, so a takeover cannot interleave between a successful
check and its delivery/outbox writes. Check actual database time at the guarded
statement, not a timestamp frozen at the start of a long transaction. Never
keep this lock during provider I/O. The SPEC must prove atomicity, not implement
a pool-bound read followed by an unrelated write.

**Reservation:** page up to 100 candidate IDs without charging attempts. Each
bounded executor obtains a concurrency slot, rate permit and breaker permit,
then uses a short transaction under the valid campaign lease to recheck a single
PENDING device's current ownership/eligibility, deadline and budget. Increment
only that row's attempt_count once and persist claim generation/time plus a
unique attempt ID before its provider call. An unresolved reservation cannot be
claimed twice under the same campaign lease generation, even by two local
executors. Match result writes to both generation and attempt ID; lease takeover
may recover an unresolved old claim only after the persisted retry floor. Do not reserve an entire waiting page. A failed/rolled-back claim
makes no call; a crash after committed claim retains the consumed attempt.
Known breaker/rate deferrals consume no attempt; uncertain calls are never
refunded. Benchmark this per-device DB cost before optimizing reservation.
Provider results update only claimed rows under that generation; SENT/DELIVERED/OPENED/
FAILED rows are never resent or regressed. A valid-lease late success may
record provider acceptance metadata after an early receipt but must preserve
the observed state. Successful result writes and ADR-05 versioned update-outbox
inserts are one guarded transaction. Receipt promotion is a separately
authorized campaign-locked path, not a provider attempt or budget reset.

**Crash/takeover:** until lease expires no second owner is eligible. After
expiry, a new owner fences old DB writes. A reserved but unconfirmed row remains
PENDING and its consumed attempt is retained; next_attempt_at set at reservation
includes at least the A-4 minimum retry floor and applicable known cooldown.
Retry only after that time and within budget/deadline. Do not claim there is
one physical provider call under arbitrary process suspension: an old external
request may survive lease loss. Client notification-ID dedup is a mitigation
to define in ADR-06; collapse keys are not exactly-once delivery.

### Alternatives rejected

Local mutexes do not coordinate replicas. Atomic counter increments without
lease predicates allow consecutive successful claimants. Holding a DB transaction
open across a complete broadcast unnecessarily pins connections and locks.

### Consequences

NEW schema: campaign fanout_complete_at, deadline_at, failed_reason, lease fields;
delivery attempt_count, next_attempt_at, last_attempt_at, claim generation,
immutable token/owner/provider identity and uniqueness. retry_count is derived
as max(attempt_count-1,0), never a separate budget. Migration dedup must preserve
evidence and identify ambiguous legacy rows rather than arbitrarily deleting
history. Exact columns/indexes/backfill/FK rules belong in the companion SPEC.

## A-11. Recovery and internal errors

### Context

Recovery must cover abandoned final attempts as well as ordinary due retries.

### Decision

Scan on startup and periodically (proposed 10s), in bounded stable pages with
fairness so repeatedly failing work cannot starve later rows. No new provider
work under an unexpired foreign lease. Eligible states include:

| Durable state | Recovery action |
| --- | --- |
| PROCESSING without complete fan-out | Reconcile legacy/inconsistent state; never infer completeness from row count |
| Complete, no valid lease, due PENDING below per-device budget and before deadline | Acquire lease, page candidates, reserve each device only after permits |
| Complete, expired lease, reserved-unconfirmed work | Fence old generation; preserve consumed budget and retry floor |
| Complete, PENDING at budget or TTL expiry | Acquire ownership and finalize affected rows without a provider call |
| Complete, all rows SENT/DELIVERED/OPENED/FAILED or empty | Idempotently finalize campaign |
| QUEUED past deadline | Admission finalizes FAILED/ttl_expired with no provider call; background recovery invokes the same guarded path if the event was quarantined |
| QUEUED with unexpired deadline and no successful admission | Bounded background admission retry invokes the same preparation transaction, independent of outbox row deletion |

The last two rows explicitly close pre-admission gaps after a durable event
was dead-lettered. Scheduler outbox covers unpublished work; a deleted outbox
alone is not proof that admission succeeded. Background admission errors use
bounded, persisted deferral and raise alerts; unsupported legacy input stays
quarantined and requires repair, not a hot retry loop.

DB errors do not permanently fail valid work. Roll back partial mutations,
retain current durable state, release/expire the lease, defer with backoff and
surface an alert if persistent. A database outage can exceed any scan interval;
do not promise recovery within one interval without capacity/availability
assumptions. Poison payload recovery and manual replay belong to ADR-02.

### Alternatives rejected

Scanning only due retries with valid budget excludes the rows that most need
finalization. Assuming the send outbox remains after publish misses quarantine
before PROCESSING. Marking every internal error FAILED makes transient DB
failure permanent business failure.

### Consequences

Recovery is independent of local queue/timers. Persistent failure remains
visible, bounded and actionable; no unconditional "never stranded" assertion
without operational availability and remediation assumptions.

## A-12. Implementation boundaries and configuration

### Context

Generated sqlc and cross-ADR transaction changes must land consistently.

### Decision

Use NEW internal/worker for injectable admission/delivery/recovery machinery.
Existing cmd/worker remains composition/lifecycle. Migration numbers are
provisional until the milestone sequence is fixed; all changes need paired
up/down migrations before make sqlc, with generated code committed together.
Regenerate mockery whenever an actually mocked interface changes.

Worker keys (proposed): PUSH_RETRY_MAX_ATTEMPTS=5, PUSH_RETRY_BASE_DELAY=10s,
PUSH_RETRY_MAX_DELAY=5m, PUSH_RECOVERY_INTERVAL=10s, PUSH_LEASE_DURATION=60s,
PUSH_LEASE_RENEW_INTERVAL=15s, PUSH_REQUEST_TIMEOUT=10s, PUSH_BATCH_SIZE=100,
WORKER_CONCURRENCY=2, WORKER_QUEUE_SIZE=32, breaker threshold=5/cooldown=30s.
Submission rate and SDK concurrency require explicit calibrated configuration
before enabling provider delivery. PUSH_TTL=1h is owned by server and scheduler
(both create durable acceptance); worker reads persisted deadline, never resets
it from its own config. IDEMPOTENCY_TTL=24h is server-owned. Validate positive
bounds and lease/timeout relationships at startup.

No API implementation docs should claim these behaviors before implementation.
When implemented, update docs/api, docs/database.md, Godoc, AGENTS.md and the
live code-review inventory with verified findings. The revised ADR-04/05 define
immutable registration/owner epochs, signed per-device receipt evidence and
event identity/version. Review shared schema and transaction/lock order
together before writing their SPECs.

### Alternatives rejected

Reusing the old SPEC or assigning irrevocable migration numbers before ordering
the work creates conflicting contracts. Keeping TTL only in worker config
cannot set an acceptance-time deadline atomically in server/scheduler.

### Consequences

This revision is still proposed. Remaining product/ops decisions: accepted
SLO, TTL/retention sign-off, quota allocation, lifecycle/FK policy, staging and
cutover. No external exactly-once or device arrival deadline is promised.

ADR-09–11 also require safe API/audit/permission boundaries, controlled data
lifecycle and sealed restore/source-epoch reconciliation. Review the combined
transaction/schema contracts before SPEC acceptance. Normal restart never
resets a delivery budget or generates a new source epoch.

## Design verification cases for the companion SPEC

| Input / failure | Condition | Expected observable result |
| --- | --- | --- |
| Fan-out interrupted halfway | Before preparation commit | No partial committed audience; retry produces one complete snapshot |
| Two preparation drivers | Same notification | Exactly one complete audience; no duplicate intents |
| Kafka replay after preparation | Provider still busy | Admission no-op; no audience expansion or budget reset |
| Two delivery claimants | One valid lease | One owner; next claimant cannot reserve overlapping work |
| Late old result | Lease expired and generation changed | DB write rejected; external duplicate uncertainty documented |
| Crash on last reserved submission | Budget consumed, result unknown | Recovery finalizes or retries according to remaining per-device budget; never loops forever |
| TTL expires while worker down | PENDING remains | Recovery finalizes without new provider I/O |
| Receipt before campaign finalization | Row already OPENED/DELIVERED | Success counted; no status regression |
| 429/RetryAfter | Minimum and header exceed jitter | Persisted retry does not precede provider floor/header |
| Token owner/generation changed | Before retry | Old campaign not sent to replacement registration; old failure cannot deactivate new token |
| Two HTTP requests share key | First tx pending/rolled back/committed | No speculative 202; retry acquires ownership or replays committed snapshot |
| Broker down after acceptance | Commit succeeded | 202 retained, key/outbox/deadline durable, no DRAFT rollback |
| Valid event quarantined before preparation | Outbox already deleted | Background admission or operator replay recovers QUEUED safely |
| 20,000-device broadcast | Healthy simulated provider plus calibrated SDK | Measured resource/rate bounds; benchmark reported separately from live-provider evidence |
