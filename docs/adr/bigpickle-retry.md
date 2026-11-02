# ADR — Bigpickle: retry and failure handling for push notification delivery

> **SUPERSEDED (2026-10-02).** This unreviewed draft has been superseded by
> [`docs/adr/adr-01-provider-retry.md`](./adr-01-provider-retry.md), which is
> the canonical ADR-01. This file is kept in place as input only; the
> canonical document's "Review notes on the draft" section records every
> deviation (migration renumbering, the A-11 internal-error correction, the
> widened recovery-scan rule, and a corrected `GetStatistics.Targeted` claim).
> **Do not implement against this file.**

**Status:** superseded by `docs/adr/adr-01-provider-retry.md` (was: proposed)
**Scope:** `internal/features/notifications` (processor, provider, service, handler),
`cmd/worker`, `internal/kafka`, `db/migrations`, `db/queries`, `internal/config`.
Companion specification: [`docs/specs/bigpickle-retry.md`](../specs/bigpickle-retry.md).

## Background (as-is, verified against current `main`)

- The API `POST /api/v1/notifications/{id}/send` claims `DRAFT → QUEUED`
  atomically (`NotificationRepository.UpdateStatusIfDraft`,
  `internal/features/notifications/service.go`) and publishes a
  `NotificationSendRequested` event to Kafka
  (`notification.send.requested`), then returns 202 immediately.
- `cmd/worker` consumes that topic in a single goroutine
  (consumer group `notification-worker-group`) and calls
  `notifications.Processor.ProcessSendRequested`; the Kafka offset is
  committed only after the processor returns `nil` (at-least-once).
- `ProcessSendRequested` makes **one** call to `provider.Send`. A
  function-level provider error marks the notification `FAILED`
  immediately — there is no classification, no retry, no backoff, no
  circuit breaker. Per-token failures are recorded as `FAILED` delivery
  rows; the notification is still marked `COMPLETED`.
- Providers return **unclassified** errors (`internal/features/notifications/provider.go`:
  "callers cannot use errors.Is to distinguish failure modes").
  `OneSignalProvider` batches all tokens in one HTTP call and has no chunking
  (batches > 2,000 player IDs are rejected by OneSignal with a 400 — recorded
  in `onesignal_provider.go`); `FCMProvider` chunks at 500 per `SendAll` call.
- The processor's idempotency guard (`alreadyProcessed`) treats
  `COMPLETED`/`FAILED` as terminal and `PROCESSING` as terminal when any
  delivery row exists; it cannot retry a partially-fanned-out notification,
  and a crash between the last delivery insert and the `COMPLETED` update
  leaves the row in `PROCESSING` forever (the guard skips it on redelivery).
- `notification_deliveries` already carries `retry_count` and
  `failed_reason` columns, and a `PENDING` status, but the processor writes
  none of them. `device_tokens.is_active` exists and
  `DeviceTokenRepository.UpdateStatus` is documented as the mechanism to
  soft-disable a token "after the push provider reports that the token is no
  longer registered to a live device"; `ListByUserIDs` returns only active
  tokens.
- `notifications` has **no** column for a failure reason, attempt count, or
  retry timing.
- There is no idempotency-key table, no metrics library in `go.mod`, and no
  bounded worker pool: one message is processed at a time, fully in the
  consumer goroutine.

The design decisions below implement the agreed requirements:
(1) classified provider errors with bounded exponential-backoff retries,
TTL, terminal failed state, and a circuit breaker; (2) idempotent send
requests enforced in PostgreSQL with a 24-hour window; (3) sustained
100 notifications/s with 1,000/s bursts via an asynchronous pipeline with a
bounded worker pool and backpressure.

---

## A-1. Retry driver: in-worker retry loop, PostgreSQL as the durable retry ledger

### Context

The requirements demand: at most 5 provider attempts per notification,
exponential backoff with full jitter, honoring `Retry-After`, a 1-hour
time-to-live, a terminal `FAILED` state carrying the last error, and a
circuit breaker. Today, the only retry mechanism in the system is Kafka
redelivery, and the processor neutralises it: on any provider failure it
marks the notification `FAILED` and returns an error, so the redelivered
event is skipped by the idempotency guard.

### Decision

Retries are driven **inside the delivery worker**, per notification:

- The processor performs up to `PUSH_RETRY_MAX_ATTEMPTS` provider
  attempts. The attempt budget and all retry timing (`attempt_count`,
  `last_attempt_at`, `next_attempt_at`, `deadline_at`) are persisted on the
  `notifications` row (new columns, see A-10), so the budget survives worker
  restarts.
- After a transient failure, the next attempt time is persisted
  (`next_attempt_at`). When it is due, the attempt is driven by an
  in-process timer that re-enqueues the notification; the worker's bounded
  pool (A-9) executes it.
- **Offset semantics change:** the consumer commits the Kafka offset as soon
  as the notification's state is *durable* — i.e. the per-device `PENDING`
  rows have been written (fresh fan-out accepted) or the notification has
  reached a terminal status. The offset is **not** committed when the
  processor returns an internal (non-provider) error (A-11).
- A **recovery scan** (worker startup + every `PUSH_RECOVERY_INTERVAL`,
  default 60s) re-enqueues any `PROCESSING` notification whose retry is due
  or whose delivery rows are all terminal. This is the durable backstop:
  after a crash, retry progression resumes from PostgreSQL, not from
  uncommitted offsets.

### Alternatives rejected

- **Kafka redelivery as the retry mechanism** (no offset commit on failure,
  let the broker redeliver): no backoff control — the message is
  redelivered as soon as the consumer loop comes around, which *is* the
  provider hammering the requirements forbid; a restart replays the whole
  partition backlog, not just the failing notification; and the current
  terminal-`FAILED`-on-first-failure guard would still have to be removed
  anyway, so this buys nothing over the in-worker loop.
- **A dedicated Kafka retry/delay topic**: Kafka has no native
  per-message delay; a delay topic needs a second consumer loop and a third
  provisioned topic (AGENTS.md: topics are created by hand,
  `AllowAutoTopicCreation: false`), and re-introduces exactly the
  cross-system coordination (outbox) the processor's Godoc already flags as
  a known gap.
- **A transactional outbox + poller**: heavier new subsystem for a problem
  the existing queue (Kafka) + store (PostgreSQL) already solve once retry
  state lives in PostgreSQL.

### Consequences

- The durability point moves from "offset commit" to "PostgreSQL state".
  `ProcessSendRequested` must remain idempotent; its idempotency is now
  stronger (per-device dedup + CAS claim, A-10) rather than weaker.
- Worst case after a crash, a due retry waits up to
  `PUSH_RECOVERY_INTERVAL` longer than it would have (bounded, documented).
- The worker gains two background drivers (retry timer, recovery scan) and a
  periodic DB read; both are cheap relative to provider calls.
- `cmd/worker`'s documented "at-least-once via uncommitted offsets" model is
  replaced by "at-least-once via durable PENDING rows + recovery scan";
  the package doc in `cmd/worker/main.go` and AGENTS.md's landmines note
  must be updated in the same change.

---

## A-2. Provider error classification at the provider boundary

### Context

The requirements split provider failures into **permanent** (invalid or
unregistered token, malformed payload, authentication failure → never
retried) and **transient** (provider unavailable, rate limited, network
timeout, 5xx → retried). Today neither provider exposes classification:
`provider.go` documents that "callers cannot use errors.Is to distinguish
failure modes", and `OneSignalProvider`'s error contract "does not carry a
failure classification" (callers must inspect logs).

### Decision

- New sentinel errors in `internal/features/notifications`:
  `ErrProviderTransient`, `ErrProviderPermanent`, with sub-reasons
  `ErrTokenInvalid`, `ErrPayloadMalformed`, `ErrAuthFailed` (wrapped with
  `%w`), plus a typed error carrying an optional `RetryAfter` duration.
- Both providers wrap errors with these sentinels **where the response is
  decoded**, so classification happens next to the provider-specific
  signal:
  - `FCMProvider` (Firebase SDK error codes): `UNAVAILABLE`/5xx,
    `RESOURCE_EXHAUSTED`/429 (with the SDK's retry delay as `RetryAfter`),
    `INTERNAL`, and deadline/timeout → transient. `NOT_FOUND`
    ("registration token not found" / unregistered) → permanent +
    `ErrTokenInvalid`. `INVALID_ARGUMENT`/400 → permanent +
    `ErrPayloadMalformed`. `PERMISSION_DENIED`/401/403 → permanent +
    `ErrAuthFailed`.
  - `OneSignalProvider`: network/DNS/timeout, 5xx, 429 (header value as
    `RetryAfter`) → transient. 401/403 → permanent + `ErrAuthFailed`. 400
    (after chunking per A-3) → permanent + `ErrPayloadMalformed`. A 2xx
    response with `recipients < submitted` or a non-empty `errors[]` (the
    H23 partial-acceptance case) → **transient**: the H23 rationale in
    `onesignal_provider.go` already prefers over-delivery on caller retry
    to silent under-delivery, and the retry is bounded by the 5-attempt /
    1-hour budget.
  - **Unclassifiable failures default to transient**, bounded by the
    attempt budget and TTL.
- `SendResult` gains an optional `RetryAfter` so per-token 429s (FCM) carry
  the provider's demand; when several failed tokens carry different values,
  the notification's retry delay uses the **maximum**.
- The stale `NotificationProvider` Godoc (flagged as a landmine in
  AGENTS.md) is rewritten as part of this change to document the
  classification contract.

### Alternatives rejected

- **Classify in the processor from error strings**: fragile (providers
  reword errors), and the FCM SDK code / HTTP status are only reliably
  available where the response is decoded.
- **Retry everything (one class)**: wastes provider quota, delays
  terminal-failure visibility, and makes "mark the token invalid and stop
  sending to it" impossible to act on safely.
- **Retry nothing (status quo)**: violates the decision outright; a
  OneSignal blip would permanently `FAILED`-mark campaigns.

### Consequences

- Both provider files gain error-wrapping; `SendResult` and the interface
  doc change (additive for consumers: new field, new sentinels).
- `MockProvider` (production package, used by tests) needs failure-injection
  capability, or a test-only provider is added, so the retry paths are
  exercisable in `go test ./internal/features/notifications/...`.
- A provider regression that stops classifying degrades to "transient
  default" — bounded, and visible via the `failed_reason` vocabulary (A-6).

---

## A-3. OneSignal sends are chunked at 2,000 players per HTTP call

### Context

`onesignal_provider.go` documents that OneSignal rejects batches larger than
2,000 `include_player_ids` with a 400. Under the classified-error design, a
400 is **permanent/malformed** — so any campaign with more than 2,000
devices would permanently fail on arrival: a self-inflicted, load-dependent
failure. `FCMProvider` already chunks at 500 (`maxFCMMessagesPerBatch`).

### Decision

`OneSignalProvider.Send` partitions the batch into sequential chunks of at
most 2,000 player IDs (mirroring the FCM pattern): one chunk = one HTTP
call, per-token results written back to their input positions. A
chunk-level failure marks only that chunk's tokens failed (with the chunk's
classification); other chunks are unaffected.

### Alternatives rejected

- **Leave un-chunked**: at the required load (large BROADCAST campaigns at
  100/s), every campaign > 2,000 devices is a guaranteed permanent failure.
- **Parallel chunk dispatch**: the FCM precedent is sequential; parallelism
  multiplies provider rate-limit pressure exactly when the breaker wants
  calm.

### Consequences

- A notification with N tokens now performs `ceil(N/2000)` provider HTTP
  calls per attempt; "one provider attempt" (A-4) means one `Send`
  invocation, which may contain several HTTP calls.
- The 2,000 limit is a property of OneSignal, documented next to the
  constant like `maxFCMMessagesPerBatch`.

---

## A-4. Backoff schedule, Retry-After, TTL, and attempt budget

### Context

The decision fixes: max 5 attempts, exponential backoff with full jitter,
honor `Retry-After` when present, stop early on TTL expiry (assume 1 hour),
and never exceed the budget. The schedule must be concrete enough to test
(A-1's recovery path re-drives attempts after restarts, so timing must be
derived from persisted state, not memory).

### Decision

- After failing attempt *i* (1-based), the backoff window is
  `min(PUSH_RETRY_MAX_DELAY, PUSH_RETRY_BASE_DELAY * 2^(i-1))` — defaults
  1s base, 60s cap → windows of 1s, 2s, 4s, 8s for a 5-attempt budget.
  **Full jitter**: the actual delay is `uniform(0, window)`.
- If the failure carries `RetryAfter` ≥ 0, the delay is `RetryAfter`
  (verbatim; it supersedes the computed window).
- The next attempt is scheduled only if `attempt_count < 5` **and**
  `now + delay <= deadline`, where `deadline = first attempt time +
  PUSH_TTL` (default 1h), persisted as `deadline_at`. If either condition
  fails, the notification is finalized as `FAILED` immediately (A-6) —
  e.g. a `Retry-After: 300s` with 59s of TTL left means no further attempt.
- The attempt counter is persisted (`attempt_count`) and incremented only by
  an atomic claim (A-10), so the budget is exact across restarts and
  concurrent drivers.
- New worker config (defaults shown): `PUSH_RETRY_MAX_ATTEMPTS=5`,
  `PUSH_RETRY_BASE_DELAY=1s`, `PUSH_RETRY_MAX_DELAY=60s`, `PUSH_TTL=1h`.

### Alternatives rejected

- **No jitter**: after a provider outage, every queued notification would
  retry in a synchronized wave at the same instant (thundering herd) — the
  breaker's half-open probe would be drowned.
- **Decorrelated jitter**: strictly more complex; at pool size ~32 and
  ≤5 attempts there is no measurable benefit over full jitter.
- **Capping `Retry-After` at the backoff window**: the decision says honor
  the provider's value; the TTL remains the outer bound instead.
- **TTL starting at Kafka publish (`RequestedAt`)**: queue wait during a
  backlog is delay, not provider failure; the TTL measures the retry window.
  Flagged as open question OQ-8.

### Consequences

- Expected retry span is ~7.5s, worst case ~30s of pure backoff for five
  attempts; the 1-hour TTL binds mainly when `Retry-After` is large.
- `notifications` gains `attempt_count`, `last_attempt_at`,
  `next_attempt_at`, `deadline_at` (A-10).
- Tests can assert delay bounds without sleep-based flakiness by injecting
  a clock (the schedule is a pure function of `attempt_count`,
  `last_attempt_at`, `deadline_at`, and `RetryAfter`).

---

## A-5. Invalid tokens: deactivate the `device_tokens` row

### Context

The decision: "for an invalid token, mark the token as invalid and stop
sending to it." The repository already models exactly this:
`device_tokens.is_active`, `DeviceTokenRepository.UpdateStatus` ("the
mechanism used to soft-disable a token … after the push provider reports
that the token is no longer registered to a live device"), and
`ListByUserIDs` — the fan-out query — returns only active tokens.

### Decision

On a per-token outcome classified `ErrTokenInvalid` (FCM `NOT_FOUND`; the
providers' local empty/too-short rejection), the processor:

1. sets `is_active = false` on the matching `device_tokens` row
   (existing `UpdateStatus` path), and
2. finalizes that delivery row as `FAILED` (terminal; never retried in a
   later attempt of the same notification).

Because fan-out fetches only active tokens, the deactivated token is
excluded from **every** future campaign automatically. For `OneSignal`
partial-acceptance failures the provider response does not attribute the
rejection to specific player IDs, so **no** deactivation is performed in
that case (the whole chunk stays `PENDING` and is retried, per A-2).

### Alternatives rejected

- **Hard delete**: destroys the delivery-history reference
  (`notification_deliveries.device_token_id`); the repository's `Delete`
  doc explicitly deprecates deletion for this use case.
- **New `invalid_reason`/`token_status` column**: `is_active` already
  expresses the state the pipeline needs; adding audit columns for *why* a
  token was deactivated is a follow-up, not a prerequisite.
- **Skip deactivation, only stop retrying this notification**: every future
  campaign would re-pay a provider 404 for the dead token — exactly what the
  decision says to prevent.

### Consequences

- A misreported "unregistered" response from the provider deactivates a
  live token until the device re-registers (provider-trust trade-off; the
  next app launch re-registers and creates a new row — `push_token` is
  unique).
- Deactivation is observable and therefore testable: the row's
  `is_active` flag and the exclusion from the next fan-out.

---

## A-6. Terminal failure state and a stable failure-reason vocabulary

### Context

"After the last attempt, store the notification in a terminal 'failed'
state with the last error, so it can be inspected and counted in metrics.
Never drop it silently." `notifications` currently has no failure-reason
column; `failed_reason` exists only per delivery row. The repo has no
metrics library, so "counted in metrics" must at least mean "countable in
the persistent store". Also, `internal/audit/constants.go` sets the
precedent that persisted strings are schema: a stable vocabulary, not free
text.

### Decision

- `notifications` gains `failed_reason TEXT`, written **only** when the
  notification reaches `FAILED`.
- The notification is finalized `FAILED` exactly when one of these holds:
  1. attempt budget exhausted with `PENDING` rows remaining →
     `retry_budget_exhausted: <last error>`;
  2. TTL expired with `PENDING` rows remaining → `ttl_expired: <last error>`;
  3. a provider-level permanent error (auth/payload) left no retryable work
     and no `SENT` row exists → `provider_auth_failed: …` or
     `provider_payload_rejected: …`;
  4. every delivery row is terminal `FAILED` (e.g. all tokens invalid) with
     no `SENT` row → `all_devices_unreachable: <last error>`.
- In every case, all still-`PENDING` delivery rows are finalized `FAILED`
  with their own `failed_reason` and final `retry_count`, so the per-device
  view is complete. The prefixes above are a **stable, closed vocabulary**
  (documented like the audit action strings) so failure classes are
  countable with plain SQL (`WHERE failed_reason LIKE 'ttl_expired:%'`).
- Finalization is idempotent: re-entering a notification whose rows are all
  terminal only (re)computes the terminal status and never re-sends.
- The processor returns success (commit) after finalization — a terminal
  notification must not loop the consumer.

### Alternatives rejected

- **A new status value (e.g. `FAILED_RETRY_EXHAUSTED`)**: the status `CHECK`
  constraint and every doc in the repo define the vocabulary; adding a
  literal touches the state machine documented in AGENTS.md and
  `docs/database.md` for no functional gain — `FAILED` + structured
  `failed_reason` carries the information.
- **Free-text `failed_reason`**: not countable; the prefix vocabulary is
  what makes "counted in metrics" true with the tooling the repo has.
- **Leaving the offset uncommitted on terminal failure**: a wasted
  redelivery round-trip for a state that can never change; the guard would
  skip it anyway.

### Consequences

- `FAILED` remains terminal (no resubmit path; open question OQ-9).
- A migration must add the column and two new sqlc queries
  (finalize-with-reason, finalize-deliveries); `docs/database.md` and
  `code-review.md` are updated in the same change (repo convention).
- `GetStatistics`/detail views need no change; operators inspect
  `notifications.failed_reason` and the per-device rows.

---

## A-7. Circuit breaker around the provider

### Context

"If the provider keeps failing, use a circuit breaker so the service stops
hammering it." There is no breaker in the repo, and no dependency that
provides one (`go.mod` has none). The provider instance is per-worker
(`cmd/worker` hardcodes `NewOneSignalProvider`), so a per-instance breaker
is the natural scope.

### Decision

- One breaker per worker process, per provider instance; states
  `closed → open → half-open → closed`.
- **Failure signal**: a provider *call* counts as a breaker failure iff it
  produced at least one transient failure **and zero** `SENT` results.
  Any call with at least one `SENT` result counts as success and resets the
  consecutive-failure counter. (Pure permanent failures — bad tokens, bad
  payload — are not the provider's fault and never trip the breaker.)
- `PUSH_BREAKER_THRESHOLD` (default 5) consecutive failures → **open** for
  `PUSH_BREAKER_COOLDOWN` (default 30s).
- While open, attempts that come due are **deferred until the reopen time
  without calling the provider** (no provider I/O, no budget consumed; the
  effective wake time is `max(next_attempt_at, reopen_time)`). If the
  deferral would pass the notification's `deadline_at`, it is finalized
  `FAILED` instead (A-6).
- On reopen: exactly one **probe** call (half-open). Probe success →
  `closed`; probe failure → open again for a full cooldown.

### Alternatives rejected

- **Shared (e.g. PostgreSQL-backed) breaker across worker replicas**:
  coordination cost and write amplification on the hot path; with K
  replicas the in-process threshold is effectively K× — acceptable, and
  documented as a known property.
- **Per-token or per-user breakers**: the failure mode being defended
  against is a global provider outage; fine-grained breakers would stay
  closed while the provider is down and trip on unrelated token churn.
- **A third-party circuit-breaker dependency**: the state machine is ~50
  lines, stateless, and testable with an injected clock; the repo currently
  introduces no such dependency.

### Consequences

- Breaker state is in-process: a worker restart starts closed (a restarted
  replica re-trips within `threshold` calls).
- During an outage longer than the TTL, campaigns finalize
  `ttl_expired:`/`retry_budget_exhausted:` — expected and observable.
- The breaker is directly testable (fake provider + clock): call counts,
  open/half-open windows, and the "success resets" rule.

---

## A-8. Idempotency for send requests, enforced in PostgreSQL

### Context

"If the same notification request arrives twice … duplicates are suppressed
by an idempotency key (client-supplied, or derived from user and
notification id), enforced with a uniqueness guarantee in the persistent
store the project already used, with a 24-hour window. A repeated key
returns the existing result and does not send again." The only store this
system uses is PostgreSQL (there is no Redis or other cache in
`docker-compose.yml` or `go.mod`). Today, a repeated `POST …/send` within
any window returns 409 (the `DRAFT → QUEUED` claim fails); nothing remembers
the original request or result.

### Decision

- `POST /api/v1/notifications/{id}/send` accepts an optional
  **`Idempotency-Key`** request header (1–255 chars, no control characters;
  malformed → 400 "invalid idempotency key"; empty header → treated as
  absent). When absent, the key is derived deterministically as
  `send:<notification_id>:<acting staff user ID>` (the API actor is the
  "user" of the request; see open question OQ-1).
- New table `notification_send_keys`
  (`key TEXT PRIMARY KEY`, `notification_id BIGINT NOT NULL`,
  `staff_user_id BIGINT NOT NULL`, `status VARCHAR(30) NOT NULL` — the
  status snapshot at claim time —, `queued_at TIMESTAMPTZ NOT NULL`,
  `created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`, `expires_at TIMESTAMPTZ
  NOT NULL` = `created_at + 24h`), indexed on `expires_at` for purging.
  The **primary key is the uniqueness guarantee**.
- The service upserts-or-fetches the key row atomically **before** the
  `DRAFT → QUEUED` claim:
  - existing row, same `notification_id` → **replay**: return the stored
    result (notification id, `QUEUED` snapshot, original `queued_at`),
    status 202 matching the original response, with response header
    `Idempotency-Replayed: true`; no Kafka publish, no state change.
  - existing row, different `notification_id` → 409
    "idempotency key already used for a different notification".
  - new row → proceed with the existing claim + publish flow; on success the
    row keeps the result snapshot.
- **Rollback rule**: if the claim fails (not `DRAFT` → the existing 409) or
  the Kafka publish fails (the existing 500 + `QUEUED → DRAFT` rollback),
  the key row is **deleted** with the rollback, so the key stays usable by
  the client's retry.
- **24-hour window**: rows expire at `expires_at`. A throttled opportunistic
  purge (at most once per minute per API process, on the send path) or an
  equivalent mechanism deletes expired rows; after purging, the key is free
  for reuse. A key is guaranteed unenforced no later than 24h + one purge
  interval.
- The success response body gains `id`, `status`, and `queued_at` in
  addition to the existing `message` (additive; the envelope comes from
  `pkg/response`).

### Alternatives rejected

- **In-process (map/cache) dedup**: lost on restart and not shared across
  API replicas — the decision explicitly requires a uniqueness guarantee in
  the persistent store.
- **Redis or another side store**: not in the deployment or in `go.mod`;
  "the persistent store the project already uses" is PostgreSQL.
- **Key = notification ID only**: the decision says client-supplied *or*
  derived from *user and notification id*; the derived form also keeps two
  staff users from colliding on one campaign's send.
- **Time-windowed unique index**: PostgreSQL cannot express uniqueness that
  expires; the standard pattern is PK + purge.

### Consequences

- **API behavior change**: a repeated send with the same key within 24h now
  returns 202 (replay) where it returned 409 before. Clients that used 409
  to detect "already sent" must poll the detail endpoint's `status` instead.
  `docs/api/notifications-api.md` must be updated in the same change
  (README contributing rule).
- One extra indexed DB round-trip on the send hot path (trivial at 100/s).
- The key table grows at the send rate for up to 24h (at a sustained 100/s,
  ≈ 8.6M rows retained); the purge keeps it bounded. See OQ-2 for
  retention tuning.
- Scheduled promotion (`cmd/scheduler`) is **out of scope** for keying: its
  `SCHEDULED → QUEUED` CAS already guarantees at-most-once publication.

---

## A-9. Bounded worker pool, bounded ready queue, backpressure by deferral

### Context

"Expected load: 100 notifications/s sustained, bursts up to 1,000/s for a
few minutes. Sending is asynchronous … delivery happens through a queue and
a bounded worker pool. When the queue is full, apply backpressure (reject
or defer new work) instead of growing without limit." Today there is no
pool: the single consumer goroutine processes one event end-to-end, which
cannot reach 100/s at realistic provider latencies, and a burst would
serialize behind one stuck provider call.

### Decision

- The consumer goroutine's only job becomes: fetch → decode → **enqueue**
  onto a bounded ready channel (capacity `WORKER_QUEUE_SIZE`, default 256).
- A fixed pool of `WORKER_CONCURRENCY` goroutines (default 32) pulls events
  and runs the processor. Sizing note: `pool ≈ target_rps × avg_processing_time`;
  32 at an assumed 200–500ms per-notification processing supports ~64–160/s
  sustained. Both are config with defaults, not required vars.
- **Backpressure = defer**: when the ready channel is full, the enqueue
  blocks, the fetch loop stops, and Kafka lag grows. The broker (with its
  retention) is the elastic buffer; worker memory stays bounded at
  `WORKER_QUEUE_SIZE + WORKER_CONCURRENCY` in-flight events.
- **Nothing is rejected at the worker**: rejecting would drop a
  notification, violating "never drop it silently". API-level
  rejection (429) is deliberately not designed — see OQ-3.
- A panic inside a pool worker is recovered and logged; the worker process
  keeps serving, and the affected notification is redriven (its state is
  durable per A-1; the offset may be uncommitted or the recovery scan will
  pick it up).

### Alternatives rejected

- **Unbounded channel / goroutine per message**: at 1,000/s bursts with
  ~0.5s processing, an unbounded design accumulates ~hundreds of concurrent
  in-flight campaigns with no ceiling and no backpressure signal.
- **Reject with 429 at the API when worker lag exceeds a threshold**:
  requires lag telemetry from worker to API (separate processes) and a
  policy the decision leaves open ("reject **or** defer"); deferring at the
  worker satisfies the requirement with zero new cross-process surface.
- **A token-bucket rate limiter at the consumer**: the pool is already the
  concurrency governor; a second knob would compete with the breaker.

### Consequences

- `cmd/worker/main.go`'s consume loop is restructured (single goroutine
  processing → producer/consumer around a channel + pool); the consumer
  group (`notification-worker-group`) and multi-replica scaling model are
  unchanged.
- Per-notification ordering still comes from the deterministic Kafka key
  (`notification_<id>`); cross-notification ordering was never guaranteed.
- Burst behaviour is observable: in-flight event count never exceeds
  `WORKER_QUEUE_SIZE + WORKER_CONCURRENCY`, and no event is lost when the
  burst ends (Kafka drains the lag).

---

## A-10. Per-device durable fan-out: `PENDING` rows, unique constraint, CAS attempt claim

### Context

The processor's Godoc documents the current trade-off: delivery rows are
written only after the provider call, the insert loop and the idempotency
check are not atomic, a mid-loop crash "leaves a partial set of delivery
rows", and "the unrecorded deliveries are lost rather than duplicated".
With retries, that trade-off becomes unacceptable: retrying a notification
whose first attempt partially succeeded would either duplicate the
successful devices (re-send everything) or lose the failed ones (skip
because rows exist). The repository doc for
`NotificationDeliveryRepository.Create` already says the unique constraint
on `(notification_id, device_token_id)` "is the intended design for
at-least-once Kafka semantics" — it just doesn't exist in the schema.

### Decision

- Migration adds `UNIQUE (notification_id, device_token_id)` to
  `notification_deliveries` (plus the `notifications` retry columns from
  A-4/A-6: `attempt_count INTEGER NOT NULL DEFAULT 0`, `last_attempt_at
  TIMESTAMPTZ`, `next_attempt_at TIMESTAMPTZ`, `deadline_at TIMESTAMPTZ`,
  `failed_reason TEXT`). The migration must **deduplicate any legacy
  duplicate pairs** before creating the index (pre-A-10 redeliveries could
  have produced them).
- **Fan-out writes one `PENDING` row per active device token before the
  first provider call** (upsert: `ON CONFLICT … DO NOTHING`, via new sqlc
  queries), then transitions rows: `PENDING → SENT` (with
  `provider_message_id`, `sent_at`) on success; `PENDING → FAILED` on a
  permanent outcome (with `failed_reason`). Transient failures leave the
  row `PENDING` but update `retry_count` and `failed_reason` for
  observability.
- **The outstanding set is always computed as the `PENDING` rows.** A
  redriven notification (timer, redelivery, recovery scan) re-sends *only*
  the `PENDING` tokens; `SENT`/terminal `FAILED` rows are never re-sent —
  the unique constraint makes accidental duplicate inserts impossible.
- **The attempt is claimed atomically**: an `UPDATE notifications SET
  attempt_count = attempt_count + 1, … WHERE id = ? AND status =
  'PROCESSING' AND attempt_count < max AND (next_attempt_at IS NULL OR
  next_attempt_at <= NOW()) AND deadline_at > NOW()` whose row-count decides
  the driver. Two concurrent drivers of one notification therefore produce
  exactly one provider call per attempt index.
- The processor entry logic replaces `alreadyProcessed`:
  `COMPLETED`/`FAILED` → skip; no delivery rows → fresh fan-out; all rows
  terminal → finalize (A-6) and skip; `PENDING` rows present → check
  budget/TTL/due-time, then claim and re-drive the `PENDING` set.

### Alternatives rejected

- **Keep the notification-level guard**: it cannot express "retry only the
  failed half", and it has a latent bug the design fixes — a crash after
  the last delivery insert but before the `COMPLETED` update leaves the
  notification stuck in `PROCESSING` forever (the guard skips it, the
  offset gets committed on the skip).
- **A separate retry-intent table**: duplicates the delivery lifecycle in
  a second table; the schema already has `PENDING` + `retry_count` +
  `failed_reason` for exactly this purpose (`docs/database.md`).
- **Dedup via `DeliveryUpdated` event IDs**: that topic feeds downstream
  analytics and is explicitly at-least-once with no event IDs
  (`internal/kafka/events.go`); dedup must live where the truth is — the
  delivery rows.

### Consequences

- New sqlc queries (upsert-PENDING batch, claim, list-PENDING, finalize,
  recovery scan, terminal-status finalize) — migrations must land **before**
  `make sqlc` (AGENTS.md rule), then both sides are committed.
- The at-least-once duplicate window is now precisely: provider accepted a
  token's push, worker crashed before the row flip to `SENT` → the
  redriven attempt re-sends that token. Unavoidable without provider-side
  idempotency (OQ-5); unchanged in kind from the current design, smaller
  in scope (one device, one campaign).
- `ListUserNotifications`' representative-priority now sees `PENDING` rows
  (cosmetic; documented). `GetStatistics.Targeted` counts `PENDING` rows
  from fan-out start — matching its doc's "total number of delivery rows
  created".

---

## A-11. Internal (non-provider) errors: redeliver, don't terminal-fail

### Context

Today, `expandTargets` failure and device-token-fetch failure both mark the
notification `FAILED` immediately and return an error — so a transient
PostgreSQL blip permanently kills the campaign (the redelivered event is
then skipped by the guard). The decision's retry scope is provider
failures, but "never drop it silently" cuts the same way for internal
errors.

### Decision

Non-provider errors (database, target expansion, token fetch, unexpected
panics) leave the notification's state as-is (usually `PROCESSING` with
whatever durable rows exist), return an error to the consumer, and the
offset is **not** committed — Kafka redelivers, and the entry logic
(A-10) resumes from the durable state. No attempt budget is consumed, and
nothing is terminal-failed by an internal error.

### Alternatives rejected

- **Status quo (immediate `FAILED`)**: a 2-second DB blip burns a campaign
  that the retry design exists to save.
- **Internal-error attempt budget**: conflates DB capacity with provider
  outages; a healthy provider and a struggling DB should not consume the
  provider budget.

### Consequences

- A *persistent* internal error redelivers indefinitely until the DB
  recovers (bounded work: the entry check is cheap, no provider I/O).
  Dead-letter/poison handling for this case is OQ-4.
- The processor's "three failure paths set FAILED" Godoc section is
  rewritten to match.

---

## A-12. Documentation and generated-code discipline for this change

### Context

AGENTS.md: Godoc is the system's map but goes stale when fixes land
(provider.go is the named landmine); `code-review.md` is a live findings
inventory; `internal/database/sqlc/` is generated-but-committed and a new
migration must exist **before** regenerating; API endpoints are documented
in `docs/api/<module>-api.md` and schema rationale in `docs/database.md`.

### Decision

The implementation change must, in one commit set: (1) add migrations
`000016_add_delivery_retry_state.up/down.sql` (notifications retry columns
+ deliveries unique constraint + legacy dedup) and
`000017_create_notification_send_keys.up/down.sql` (both with down
migrations, per repo rule); (2) add the new queries to
`db/queries/{notifications,notification_deliveries}.sql` (+ a new
`db/queries/notification_send_keys.sql`) and regenerate with `make sqlc`;
(3) rewrite the `NotificationProvider` Godoc to the new classification
contract (replacing the stale text); (4) update `cmd/worker/main.go`'s
delivery-semantics doc, `docs/api/notifications-api.md` (idempotency key,
replay response, new 409/400 cases, response fields), `docs/database.md`
(new columns, `PENDING` semantics, key table), and add the new findings /
close the superseded H27 note in `code-review.md`.

### Alternatives rejected

- **Split docs into a later commit**: the repo's convention (point-fixes
  that update `code-review.md` in the same commit) shows stale docs are
  the recurring defect, not the code.

### Consequences

- The migration numbers assume `000015_seed_roles` is still the latest
  (verified in `db/migrations/`); adjust if another migration lands first.
- `go tool mockery` is unaffected unless the repository interfaces gain
  mocked methods — the new query surface hangs off `sqlc.Querier`, which
  the segments/staff mocks already absorb via regeneration.
