# Spec — Bigpickle: retry and failure handling for push notification delivery

> **SUPERSEDED (2026-10-02).** The companion ADR was superseded by
> [`docs/adr/adr-01-provider-retry.md`](../adr/adr-01-provider-retry.md); this
> spec will be replaced by the not-yet-written
> `docs/specs/adr-01-provider-retry.md`. The canonical ADR's "Verification
> notes" state that this draft's acceptance table (§13) is the starting point
> for the new spec, to be re-verified case-by-case — in particular case 29
> (internal-error redrive), which the canonical A-11 + ADR-02 change from
> "redeliver until the DB recovers" to "bounded consumer retry, then
> dead-letter, then recovery-scan drive". **Do not implement against this
> file.**

**Status:** superseded by pending `docs/specs/adr-01-provider-retry.md` (was: proposed)
**Companion ADR:** [`docs/adr/bigpickle-retry.md`](../adr/bigpickle-retry.md) (itself superseded — see banner above)
**Affected systems:** API server (`cmd/server`, feature
`internal/features/notifications`), delivery worker (`cmd/worker`),
PostgreSQL, Kafka, push provider (OneSignal default / FCM alternative).

This specification describes **observable behavior**: HTTP responses,
PostgreSQL state, Kafka events, provider call counts, and timing bounds.
Anything a tester cannot observe from outside (channel sizes, goroutine
counts) is only referenced as the source of an observable guarantee.

---

## 1. Scope and goals

In scope:

1. **Provider failure handling** — classify errors (permanent vs transient),
   retry transient failures with exponential backoff + full jitter, honor
   `Retry-After`, bound retries at 5 attempts and a 1-hour TTL, finalize
   failed notifications with the last error, deactivate invalid device
   tokens, and stop hammering a failing provider via a circuit breaker.
2. **Send-request idempotency** — suppress a repeated send request with an
   idempotency key (client-supplied or derived), enforced by a PostgreSQL
   uniqueness constraint, 24-hour window, replay returns the original
   result.
3. **Load behavior** — asynchronous delivery at 100 notifications/s
   sustained with 1,000/s bursts; bounded in-flight work in the worker;
   backpressure by deferral, never by dropping.

Out of scope: provider-side idempotency, dead-letter queues, an API-level
reject policy, metrics endpoints, and any change to the scheduler's
scheduled-promotion path (its `SCHEDULED → QUEUED` CAS already guarantees
at-most-once publication).

## 2. Roles

| Role | Process | Responsibility (unchanged unless noted) |
|---|---|---|
| API | `cmd/server` | Authoring campaigns; `POST /api/v1/notifications/{id}/send` claims `DRAFT → QUEUED` and publishes `NotificationSendRequested` to Kafka topic `notification.send.requested`, returning immediately (**changed**: idempotency-key handling, §6) |
| Worker | `cmd/worker` | Consumes the send topic (group `notification-worker-group`), expands targets, dispatches via the provider, records `notification_deliveries`, publishes `DeliveryUpdated` to `notification.delivery.updated` (**changed**: bounded pool, retry loop, breaker, recovery scan, §4/§7/§8) |
| Scheduler | `cmd/scheduler` | Promotes due `SCHEDULED → QUEUED`; no change |
| Provider | OneSignal REST / FCM (Firebase SDK) | External push delivery; both implement the `NotificationProvider` interface in `internal/features/notifications` (**changed**: error classification, §3) |
| Store | PostgreSQL | Durable state: `notifications`, `notification_deliveries`, `device_tokens`, new `notification_send_keys` (§9) |

## 3. Error classification (R1)

A provider call returns one outcome per input token (`SENT` or `FAILED` with
a reason) plus an optional function-level failure. Every failure is
classified into exactly one of two classes:

| Class | Sub-reason | Effect |
|---|---|---|
| **Permanent** | `token_invalid` | The affected delivery row is finalized `FAILED`; if the provider identified the specific token, that `device_tokens` row is set `is_active = false` and the token is excluded from all future fan-outs (§4.4). No retry of that token. |
| **Permanent** | `auth_failed` | All outstanding tokens of the notification are finalized `FAILED`; the notification is finalized `FAILED` (`provider_auth_failed:`). No retry. No token deactivation. |
| **Permanent** | `payload_malformed` | Same as `auth_failed` but `provider_payload_rejected:`; no token deactivation. |
| **Transient** | `provider_unavailable`, `rate_limited`, `network`, `timeout`, `5xx`, or unclassifiable | The tokens stay `PENDING`; the notification is retried under §4. |

Provider-signal mapping (normative for the shipped providers):

| Signal | Class | Notes |
|---|---|---|
| FCM/FCM-APNs: registration token not found / unregistered (SDK `NOT_FOUND`, 404) | permanent / `token_invalid` | Per-token; deactivates that token |
| FCM: `INVALID_ARGUMENT` / 400 | permanent / `payload_malformed` | Batch-level |
| FCM: `PERMISSION_DENIED` / 401 / 403 | permanent / `auth_failed` | Batch-level |
| FCM: `UNAVAILABLE`, 5xx, `INTERNAL` | transient | |
| FCM: `RESOURCE_EXHAUSTED` / 429 | transient | Carries `RetryAfter` from the SDK's retry delay |
| Any provider: network error, DNS failure, client timeout | transient | |
| OneSignal: 429 | transient | `RetryAfter` from the `Retry-After` header |
| OneSignal: 5xx | transient | |
| OneSignal: 401 / 403 | permanent / `auth_failed` | e.g. misconfigured `ONESIGNAL_API_KEY` |
| OneSignal: 400 | permanent / `payload_malformed` | After §4.5 chunking, a 400 indicates a genuinely malformed request |
| OneSignal: 2xx with `recipients < submitted` or non-empty `errors[]` (partial acceptance) | transient | OneSignal does not attribute the rejection to specific player IDs; the whole chunk stays `PENDING` and is retried (over-delivery on retry is accepted, per the H23 rationale in `onesignal_provider.go`). No token deactivation. |
| Unclassifiable failure | transient | Bounded by the attempt budget and TTL |

Local pre-dispatch rejection of an empty or too-short push token (the
providers' existing length heuristic) is **permanent / `token_invalid`** and
deactivates the token.

## 4. Retry policy (R2)

### 4.1 Attempt budget

- A notification gets **at most 5 provider attempts** in total
  (`PUSH_RETRY_MAX_ATTEMPTS`). An attempt is one `provider.Send`
  invocation (which may contain several HTTP calls, §4.5).
- The budget is **durable**: `notifications.attempt_count` is incremented
  only by an atomic claim (§4.6) and is read back on re-entry, so the
  budget survives worker restarts and concurrent drivers.
- Attempt 1 is the initial fan-out.

### 4.2 Backoff schedule

After failing attempt *i* (1-based), the delay window is

```
window(i) = min(PUSH_RETRY_MAX_DELAY, PUSH_RETRY_BASE_DELAY * 2^(i-1))
```

Defaults: base 1s, cap 60s → windows 1s, 2s, 4s, 8s (there are 4 inter-attempt
delays within a 5-attempt budget). **Full jitter**: the actual delay is a
uniform random value in `[0, window(i))`.

If the failed attempt's error carries a `Retry-After` value `r ≥ 0`, the
delay is `r` instead (verbatim; it supersedes the window). When several
failed tokens carry different `Retry-After` values, the maximum applies.

### 4.3 Time-to-live

- `deadline = time of first attempt + PUSH_TTL` (default 1h), persisted as
  `notifications.deadline_at`.
- A next attempt is scheduled only if it can **start** at or before the
  deadline. If `attempt_count` is already 5, or `now + delay > deadline`
  (e.g. a large `Retry-After`), no further attempt is made and the
  notification is finalized `FAILED` (§5).
- Queue wait time (time spent in Kafka before first processing) does not
  count against the TTL (open question OQ-8).

### 4.4 Per-device outcomes and token deactivation

- Before the first provider call, one `PENDING` delivery row is written per
  active device token of the resolved audience (`notification_deliveries`,
  upsert-safe against the unique `(notification_id, device_token_id)`
  constraint).
- Per attempt, results finalize rows: `PENDING → SENT` (with
  `provider_message_id` and `sent_at`) or `PENDING → FAILED` (with
  `failed_reason`) for permanent outcomes. Transient outcomes leave the row
  `PENDING` but update `retry_count` and `failed_reason` (observable
  progress; the row remains retryable).
- **`token_invalid`** additionally sets `device_tokens.is_active = false`
  on the matched row. Since the fan-out query returns only active tokens,
  the deactivated token is absent from every future campaign. The row is
  never deleted.
- OneSignal partial-acceptance failures deactivate **no** tokens (the
  response does not attribute the rejection).

### 4.5 What is retried

Only the tokens whose delivery row is still `PENDING` are sent on the next
attempt. Tokens already `SENT` or permanently `FAILED` are never re-sent, on
any re-drive path (timer, Kafka redelivery, recovery scan). The unique
constraint makes a duplicate delivery row for
`(notification_id, device_token_id)` impossible.

The provider chunks large batches internally (FCM: 500 per call — existing;
OneSignal: new, 2,000 per call). A chunk-level transient failure marks only
that chunk's tokens `PENDING`-failed; other chunks' outcomes stand.

### 4.6 Attempt claim (no double-driving)

An attempt is claimed by an atomic conditional update on the
`notifications` row that increments `attempt_count` only when
`status = 'PROCESSING'`, `attempt_count < 5`, the retry is due
(`next_attempt_at` is NULL or ≤ now), and the deadline has not passed.
Consequence: two drivers processing the same notification concurrently
(timer + redelivery, or two worker replicas redelivering) produce **exactly
one** provider call per attempt index; the loser exits without calling the
provider.

### 4.7 Re-drive sources

A due attempt may be started by any of: the in-process retry timer (normal
path), a redelivered Kafka event (offset not yet committed, or consumer
group rebalance), or the worker's recovery scan (§8). All three enter the
same processor entry logic and are deduplicated by §4.5 + §4.6.

## 5. Notification terminal states and failure reasons (R3)

The processor finalizes a notification when no retryable work remains:

| Condition | Terminal status | `notifications.failed_reason` |
|---|---|---|
| No `PENDING` rows remain and at least one `SENT` row exists (any mix of `SENT` and permanently-`FAILED` rows) | `COMPLETED` | `NULL` |
| No `PENDING` rows, zero `SENT` rows, all failures permanent (e.g. every token invalid) | `FAILED` | `all_devices_unreachable: <last error>` |
| Attempt budget exhausted with `PENDING` rows remaining | `FAILED` | `retry_budget_exhausted: <last error>` |
| Deadline expired with `PENDING` rows remaining (including when a `Retry-After` would start past the deadline, or a breaker deferral would pass the deadline) | `FAILED` | `ttl_expired: <last error>` |
| Provider-level permanent error (auth/payload) with no `SENT` row | `FAILED` | `provider_auth_failed: <last error>` / `provider_payload_rejected: <last error>` |

- On `FAILED`, every remaining `PENDING` delivery row is finalized `FAILED`
  with its own `failed_reason` and final `retry_count` — no device is left
  unaccounted for.
- The prefixes `retry_budget_exhausted:`, `ttl_expired:`,
  `all_devices_unreachable:`, `provider_auth_failed:`,
  `provider_payload_rejected:` are a **stable, closed vocabulary**
  (countable via `WHERE failed_reason LIKE '<prefix>%'`), analogous in
  intent to the audit action strings in `internal/audit/constants.go`.
- Terminal `COMPLETED`/`FAILED` notifications are skipped on any re-drive
  (existing guard behavior, preserved).
- No notification is ever silently dropped: every processed notification
  ends in `COMPLETED` or `FAILED` with complete per-device rows, or stays
  `PROCESSING` with complete `PENDING` rows (both states queryable).
- Existing behavior preserved: a resolved audience with no users or no
  active tokens finalizes `COMPLETED` with zero delivery rows.

## 6. Idempotency of send requests (R4)

Applies to `POST /api/v1/notifications/{id}/send` only.

### 6.1 Key resolution

1. If request header `Idempotency-Key` is present and valid (1–255
   characters, no control characters) → the key is its value.
   Invalid (too long / control characters) → **400** `invalid idempotency
   key`; an empty header is treated as absent.
2. Otherwise → derived key `send:<notification_id>:<acting staff user ID>`
   (the API actor; OQ-1).

### 6.2 Enforced behavior

The key is enforced by the `notification_send_keys` primary key
(§9). For a request with key `K` targeting notification `N`:

| Condition | Response | Side effects |
|---|---|---|
| Row for `K` exists, its `notification_id = N`, age < 24h (row not yet purged) | **202**, body carries the stored result: `id = N`, `status` snapshot (`QUEUED`), original `queued_at`, `message`; response header **`Idempotency-Replayed: true`** | None: no Kafka event, no state change |
| Row for `K` exists, its `notification_id ≠ N` | **409** `idempotency key already used for a different notification` | None; `N` remains `DRAFT` |
| No row for `K` | Proceed: insert row, then the existing `DRAFT → QUEUED` claim + Kafka publish; **202** with `id`, `status` (`QUEUED`), `queued_at`, `message` | One `NotificationSendRequested` event; key row retains the result snapshot |
| No row for `K`, claim fails (not `DRAFT`) | **409** (existing not-DRAFT semantics) | Key row is **deleted** (key remains usable) |
| No row for `K`, publish fails | **500** `internal server error` (existing literal, per repo 500 rule) | `QUEUED → DRAFT` rollback **and** key row deleted (key remains usable) |

Notes:

- A repeated send with the same key within 24h is a **202 replay**, where
  it is a 409 today. Clients detecting "already sent" must read the detail
  endpoint's `status`, not rely on 409.
- The 24-hour window: `expires_at = created_at + 24h`. Expired rows are
  purged by a throttled opportunistic delete on the send path (at most once
  per minute per API process) or an equivalent mechanism. A key is
  guaranteed unenforced no later than 24h + one purge interval; between
  24h and the next purge the key may still be enforced (acceptable — the
  window is a minimum lifetime for the result, not an expiry clock for
  enforcement).
- Replay returns the **stored** result snapshot (the status at claim time),
  not the notification's current live status.

## 7. Asynchronous pipeline, bounded pool, backpressure (R5)

- The API send path is unchanged in its asynchrony: it returns after the
  Kafka publish (existing `RequireAll` producer). The idempotency upsert
  adds one indexed DB round-trip.
- The worker processes events on a **fixed pool of `WORKER_CONCURRENCY`
  workers** (default 32) fed by a **bounded ready queue**
  (`WORKER_QUEUE_SIZE`, default 256).
- **Backpressure by deferral:** when the ready queue is full, the consumer
  blocks; fetching stops; Kafka lag grows. The broker's retention is the
  elastic buffer for bursts. Observable guarantees:
  - In-flight events in the worker never exceed
    `WORKER_QUEUE_SIZE + WORKER_CONCURRENCY`.
  - No event is dropped or rejected by the worker.
  - A burst of 1,000/s for a few minutes is absorbed as lag and drained at
    pool capacity; every enqueued notification is eventually processed
    (or, per §3–§5, finalized) exactly per the at-least-once contract.
- A panic inside one worker is contained (logged, worker survives); the
  affected notification is redriven via §4.7/§8 and is not lost.
- Worker replicas scale via the existing Kafka consumer group; per
  notification, ordering is preserved by the deterministic key
  `notification_<id>` (unchanged).

## 8. Circuit breaker (R6)

One breaker per worker process, per provider instance; default config
below. States: `closed`, `open`, `half-open`.

| Rule | Behavior |
|---|---|
| Failure signal | A provider call counts as a breaker failure iff it produced ≥ 1 transient failure **and zero** `SENT` results. A call with ≥ 1 `SENT` counts as success and resets the consecutive-failure counter. Pure permanent failures never count. |
| Open | After `PUSH_BREAKER_THRESHOLD` (default 5) consecutive failures → open for `PUSH_BREAKER_COOLDOWN` (default 30s). |
| While open | Due attempts are deferred to the reopen time **without calling the provider** (no provider I/O, no attempt-budget consumption). Effective wake time is `max(next_attempt_at, reopen_time)`. If that time passes the notification's deadline, it is finalized `ttl_expired:` instead. |
| Half-open | On reopen, exactly one probe call. Probe success → `closed` (counter reset). Probe failure → open for a full cooldown again. |

Observable properties: while open, provider call count for that worker is
0 until the cooldown elapses; the first call after a cooldown is the probe.
Breaker state is in-process: a restarted worker starts closed.

## 9. Schema and API surface (R7)

New migrations (numbers assume `000015_seed_roles` is the latest in
`db/migrations/`; adjust if another lands first):

- `000016_add_delivery_retry_state`:
  - `notifications` columns: `attempt_count INTEGER NOT NULL DEFAULT 0`,
    `last_attempt_at TIMESTAMPTZ`, `next_attempt_at TIMESTAMPTZ`,
    `deadline_at TIMESTAMPTZ`, `failed_reason TEXT`.
  - `notification_deliveries`: `UNIQUE (notification_id, device_token_id)`
    after deduplicating any legacy duplicate pairs.
- `000017_create_notification_send_keys`:
  `key TEXT PRIMARY KEY`, `notification_id BIGINT NOT NULL`,
  `staff_user_id BIGINT NOT NULL`, `status VARCHAR(30) NOT NULL`,
  `queued_at TIMESTAMPTZ NOT NULL`, `created_at TIMESTAMPTZ NOT NULL
  DEFAULT NOW()`, `expires_at TIMESTAMPTZ NOT NULL`, plus an index on
  `expires_at`.

Both with `.down.sql` counterparts (repo rule). New queries in
`db/queries/` (attempt claim, pending-row upsert/list, delivery finalize,
terminal-status finalize, recovery scan, key upsert/fetch/delete, expired
key purge) followed by `make sqlc`; generated code committed alongside.

API surface changes (`POST /api/v1/notifications/{id}/send`):

- New optional request header `Idempotency-Key` (§6.1).
- 202 response body gains `id`, `status`, `queued_at` (additive to the
  existing `message`); replay responses add header `Idempotency-Replayed:
  true`.
- New 400 case (invalid key) and 409 case (key used for a different
  notification).

`docs/api/notifications-api.md` and `docs/database.md` are updated in the
same change set.

## 10. Crash and recovery semantics (R8)

- The Kafka offset is committed once the notification's state is **durable**:
  fresh fan-out accepted (all `PENDING` rows written) or a terminal status
  reached. It is not committed on internal errors (the event redelivers).
- Worker startup and every `PUSH_RECOVERY_INTERVAL` (default 60s) run a
  recovery scan: `PROCESSING` notifications with `PENDING` rows and a due
  (or NULL) `next_attempt_at` within budget/deadline, plus `PROCESSING`
  notifications whose rows are all terminal (finalize). Candidates are
  re-enqueued and enter the same entry logic (§4.6 dedups against any live
  driver).
- Consequences:
  - After a crash, a due retry is redriven within one recovery interval;
    the attempt budget continues from the persisted `attempt_count`.
  - Worst-case duplicate window (inherent to at-least-once, unchanged in
    kind from today): the provider accepted a token's push, the worker
    crashed before flipping the row to `SENT` → the redriven attempt
    re-sends that one token. No other duplicate is possible (§4.5).
  - A notification can never be stranded in `PROCESSING` indefinitely: the
    recovery scan finalizes it when its rows are all terminal, and the
    budget/TTL finalize it otherwise.

## 11. Configuration

All new variables are optional and take the defaults shown (unlike existing
worker vars, missing values are not fatal):

| Variable | Default | Meaning |
|---|---|---|
| `WORKER_CONCURRENCY` | `32` | Delivery worker pool size |
| `WORKER_QUEUE_SIZE` | `256` | Bounded ready-queue capacity between consumer and pool |
| `PUSH_RETRY_MAX_ATTEMPTS` | `5` | Provider attempts per notification |
| `PUSH_RETRY_BASE_DELAY` | `1s` | Backoff base |
| `PUSH_RETRY_MAX_DELAY` | `60s` | Backoff cap |
| `PUSH_TTL` | `1h` | Retry deadline from first attempt |
| `PUSH_BREAKER_THRESHOLD` | `5` | Consecutive failures to open the breaker |
| `PUSH_BREAKER_COOLDOWN` | `30s` | Open duration |
| `PUSH_RECOVERY_INTERVAL` | `60s` | Recovery-scan period |
| `IDEMPOTENCY_TTL` | `24h` | Send-key lifetime (API side) |

Sizing note: `WORKER_CONCURRENCY ≈ target_rps × avg_processing_time`; the
default assumes 200–500 ms per-notification processing. Calibrate against
provider quotas (OQ-2).

## 12. Open questions

1. **OQ-1 — "user" in the derived key.** The spec derives the key from the
   acting **staff** user (the API actor). If "user" was meant to be a
   mobile-app user, the derived form is undefined for a campaign send
   (there is no single mobile user in the request) and the key would have
   to be client-supplied always. Confirm the staff-actor reading.
2. **OQ-2 — Pool calibration and key-table retention.** OneSignal/FCM QPS
   quotas are unknown; `WORKER_CONCURRENCY=32` is an estimate. At sustained
   100 sends/s the `notification_send_keys` table retains ~8.6M rows/day;
   confirm the 24h retention (vs. a shorter TTL for the dedup window) and
   purge frequency.
3. **OQ-3 — API-level rejection policy.** The spec defers (Kafka lag is the
   buffer). Should the API ever answer 429/503 when worker lag exceeds a
   threshold? Requires lag telemetry across processes; not designed.
4. **OQ-4 — Poison handling for persistent internal (DB) errors.** Redelivery
   loops until the DB recovers. Is a dead-letter topic required?
5. **OQ-5 — Provider-side idempotency.** The §10 duplicate window (crash
   between provider accept and row flip) can only be closed with
   provider-side dedup (e.g. FCM/OneSignal message tags). Worth a follow-up
   investigation; currently accepted as at-least-once.
6. **OQ-6 — OneSignal player attribution.** OneSignal's partial-acceptance
   response does not name the rejected player IDs. Should the provider
   parse the `errors[]` text and/or purge known-bad player IDs via
   OneSignal's player-delete endpoint? Until then, no token deactivation
   happens for OneSignal failures (§4.4).
7. **OQ-7 — Metrics.** No metrics library exists in `go.mod`; failure
   classes are countable in PostgreSQL (§5 vocabulary). Add a Prometheus
   endpoint / counters later?
8. **OQ-8 — TTL origin.** The deadline starts at the **first attempt**, so
   queue wait is excluded. Should it instead start at request time
   (`RequestedAt` / claim)?
9. **OQ-9 — Resubmitting a FAILED notification.** Terminal `FAILED` is not
   resubmittable (no `FAILED → DRAFT` path exists today). Provide an
   operator "retry" action? Not designed.

## 13. Acceptance cases

Table-driven cases a tester can turn into Go tests. Mocks/fixtures:
test double for the provider (injected `NotificationProvider`), controlled
clock, PostgreSQL (testcontainers or a real DB via `make`-level setup),
Kafka producer stub. "Provider calls" counts `provider.Send` invocations
for the notification in question.

| # | Input | Condition | Expected result |
|---|---|---|---|
| 1 | Notification with 3 active tokens; provider fails attempt 1 with a transient error (503), succeeds on attempt 2 | Default retry config | Notification `COMPLETED`; 3 `SENT` delivery rows; `attempt_count = 2`; exactly 2 provider calls; delay between attempts in `[0, 1s]` (window for i=1) |
| 2 | Notification with 3 tokens; FCM returns `NOT_FOUND` (unregistered) for token X only | 1 attempt | X's row `FAILED` with reason containing "unregistered"/"not found"; `device_tokens.is_active = false` for X; the other 2 rows `SENT`; notification `COMPLETED`; exactly 1 provider call; X is absent from any subsequent fan-out for that user |
| 3 | Provider returns 401 (auth) for every token | 1 attempt | All outstanding rows `FAILED`; notification `FAILED`; `failed_reason` prefix `provider_auth_failed:`; exactly 1 provider call; no token deactivation |
| 4 | Provider returns 400 (malformed payload) | 1 attempt | All rows `FAILED`; notification `FAILED`; `failed_reason` prefix `provider_payload_rejected:`; exactly 1 provider call |
| 5 | Provider always fails transiently (503) | 5-attempt budget | Exactly 5 provider calls; notification `FAILED`; `failed_reason` prefix `retry_budget_exhausted:` containing the last error; all rows `FAILED` with final `retry_count`; no further provider calls on subsequent re-drives; offset committed (no redelivery loop) |
| 6 | Provider always answers 429 with `Retry-After: 300s`; `PUSH_TTL` shortened to 10s in test config | 1 attempt | No second attempt (`now + 300s > deadline`); notification `FAILED` with prefix `ttl_expired:`; exactly 1 provider call |
| 7 | Provider fails attempt 1 with 429 and `Retry-After: 3s` | Controlled clock | Second attempt starts in `[3s, 3.5s]` after the first (Retry-After honored, not the 1s backoff window); if it succeeds, notification `COMPLETED` with `attempt_count = 2` |
| 8 | 200 independent notifications each failing exactly once with a transient error; budget 2; base 1s, cap 60s | Statistical | Every observed inter-attempt delay is in `[0, 1s]`; observed delays are not all identical (jitter) |
| 9 | Breaker threshold 5, cooldown 3s (test config); 5 notifications each fail fully on their single attempt (all-transient batch, no `SENT`) | Worker process | The 6th notification's attempt is deferred: 0 provider calls for it until the 3s cooldown elapses; after cooldown, exactly 1 probe call occurs; if the probe succeeds, the 7th notification calls the provider immediately |
| 10 | A provider call returns 1 `SENT` + 4 permanently-`FAILED` tokens, repeated ≥ threshold times | Breaker config as default | Breaker stays `closed` (a call with a success is not a failure); subsequent notifications are not deferred |
| 11 | Breaker open with cooldown 30s; a notification's retry comes due while open, and the remaining TTL exceeds the reopen time | Open window | No provider call during the window; when the breaker reopens, the notification gets its full remaining attempt budget (attempt count was not consumed by the deferral) |
| 12 | `POST /notifications/{id}/send` with `Idempotency-Key: k1` on a `DRAFT` notification; then the same request again within 24h | API + Kafka | First: 202 with `id`, `status=QUEUED`, `queued_at`; one Kafka event on the send topic. Second: 202 with identical `id` and `queued_at`, response header `Idempotency-Replayed: true`; total Kafka events for the notification = 1; notification still `QUEUED` |
| 13 | Same as 12 but with **no** `Idempotency-Key` header, same staff user | 24h window | Second request is a replay of the derived key `send:<id>:<staff>`: 202 + `Idempotency-Replayed: true`, no second Kafka event (previously this case was 409) |
| 14 | Notification already sent by staff A (not `DRAFT`); staff B calls send with no header | Derived keys differ (`send:<id>:<B>`) | 409 (existing not-DRAFT semantics); no key row consumed by B; no Kafka event |
| 15 | Key `k` was used for notification A (sent); staff now sends notification B with `Idempotency-Key: k` | B is `DRAFT` | 409 "idempotency key already used for a different notification"; B remains `DRAFT`; no Kafka event for B |
| 16 | Kafka broker down; staff sends a `DRAFT` notification with a fresh key; then the broker recovers and the staff retries with the same key | Publish failure path | First call: 500 `internal server error`; notification back to `DRAFT`; key row deleted. Retry: 202; one Kafka event; key row created |
| 17 | Key `k` used 24h ago for notification A (row backdated / expired); purge has run; staff sends `DRAFT` notification B with `Idempotency-Key: k` | Expired key | 202 (key treated as new); one Kafka event for B — key reuse after the 24h window is allowed |
| 18 | `Idempotency-Key` header with 300 characters | Malformed key | 400 `invalid idempotency key`; no state change; no Kafka event |
| 19 | 1,000 events published to the send topic while all workers are blocked (provider hang); pool 2, queue 4 | Backpressure | In-flight events in the worker never exceed 6; no event lost; after the provider unblocks, all 1,000 notifications are processed (provider calls = 1,000 ± already-terminal) and end in a terminal status |
| 20 | Notification mid-fan-out: 1 of 4 tokens `SENT`, 3 `PENDING`; the Kafka event for it is redelivered (offset not committed, simulated crash/rebalance) | Redrive | The redriven attempt sends exactly the 3 `PENDING` tokens (provider receives 3, not 4); no new delivery rows (unique constraint); the `SENT` row is untouched |
| 21 | Two goroutines (simulating timer + redelivery) start the same attempt for one notification concurrently | CAS claim | Exactly 1 provider call for that attempt index; `attempt_count` increases by 1 |
| 22 | Notification at `attempt_count = 3` with `PENDING` rows and a due `next_attempt_at`; kill the worker, restart it | Recovery scan (interval configurable for the test) | Within one recovery interval the notification is redriven; attempts continue from 4 (budget not reset); total provider calls for its lifetime ≤ 5 |
| 23 | All tokens of a notification are unregistered (FCM `NOT_FOUND` for every token) | 1 attempt | Every row `FAILED`; every token `is_active = false`; notification `FAILED` with prefix `all_devices_unreachable:`; exactly 1 provider call; no retries |
| 24 | Audience resolves to users whose tokens are all inactive | Fan-out | Notification `COMPLETED`; zero delivery rows (existing behavior preserved) |
| 25 | 1 token `SENT`, 1 token permanently `FAILED` (invalid) | Mixed outcome | Notification `COMPLETED`; `failed_reason` is `NULL`; per-device rows `SENT` + `FAILED` with reason; no retries |
| 26 | Notification in `PROCESSING` whose delivery rows are all terminal (simulated crash between last row flip and status update); event redelivered or recovery scan runs | Finalize on re-entry | Notification reaches `COMPLETED` (if any `SENT`) or `FAILED` accordingly; no provider call; no row changes beyond the status/`failed_reason` |
| 27 | BROADCAST notification with 2,500 active tokens, OneSignal provider pointed at a fake endpoint | Chunking | The provider issues 2 HTTP calls (2,000 + 500 player IDs); 2,500 `SENT` rows; notification `COMPLETED` |
| 28 | OneSignal returns 2xx with `recipients < submitted` (partial acceptance, no attribution) | 1st attempt | Whole chunk's tokens stay `PENDING`; classified transient; a 2nd attempt occurs (retry); no token deactivated |
| 29 | Target-expansion DB query fails (injected error) on first processing | Internal error | Notification is **not** marked `FAILED`; the event is redelivered (offset not committed); after the DB error clears, the notification is processed normally to a terminal status |
| 30 | Terminal `FAILED` notification's event is redelivered one more time | Terminal guard | Skipped without provider call; status and `failed_reason` unchanged; offset committed |
