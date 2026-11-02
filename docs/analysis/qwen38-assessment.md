# Architectural Assessment — In-House Push Notification System (OneSignal Replacement)

**Author:** Qwen3.8 (architect) · **Date:** 2026-10-01 · **Status:** Phase 1 (assessment only)
**Module under assessment:** Go module `hello` (API server `:cmd/server` + Kafka worker `:cmd/worker` + scheduler `:cmd/scheduler` + `:cmd/bootstrap`).

---

## 1. Method and Evidence Rules

- Findings cite `path` (+ symbol/line). Where a search returned nothing, "none found" is
  stated with the pattern searched.
- `code-review.md` (C1–C20, H1–H46, M1–M47, L1–L20) is treated as a live inventory.
  **It predates the recent fix pass.** Verified against current `main`:
  C1, C2, C8, C9–C13, C17, C20, H19–H29, H45, H46 are **fixed in code**
  (evidence per item below or in AGENTS.md). H43 (server graceful shutdown) was
  re-checked and is **still open**. Findings listed as *open* here were
  re-verified against the current source unless marked "Unverified".
- Build/test/CI state claims that cannot be confirmed from files are marked
  **"Unverified (needs execution)"**; the user's statements (suite RED on main,
  no CI) are taken as given.
- No `.env` file, credential, or key file contents were read. A local `.env` exists
  at repo root (gitignored via `.gitignore:34`); path noted only.
- Severity: **Blocker / High / Medium / Low** + one-line impact.

### Verified-fixed cross-check (spot audit of `code-review.md` vs current code)

| Finding | Status on `main` | Evidence |
|---|---|---|
| C1 (timing attack on API key) | fixed | `internal/middleware/api_key.go:126` uses `subtle.ConstantTimeCompare` |
| C2 (role claim on refresh) | fixed | `internal/features/auth/service.go:322` passes `staff.RoleID` |
| C8 (JWT active check) | fixed | `cmd/server/main.go:116-119` wires `StaffUserRepository` into JWT middleware; per-request active check documented in `cmd/server/main.go` package doc |
| C12 (Send missing QUEUED claim) | fixed | `NotificationService.Send` claims `DRAFT → QUEUED` atomically via `UpdateStatusIfDraft` (`internal/features/notifications/service.go:1019`) |
| C13 (worker never published updates) | fixed | `processor.go:424` calls `p.producer.PublishDeliveryUpdated` |
| C17 (rune message key) | fixed | `internal/kafka/producer.go:168` uses `strconv.FormatInt` |
| H45/H46 (auto-topic, RequireOne) | fixed | `internal/kafka/producer.go:89-91` — `AllowAutoTopicCreation: false`, `RequiredAcks: RequireAll` |
| H43 (no graceful shutdown, server) | **still open** | `cmd/server/main.go:162` — bare `log.Fatal(server.ListenAndServe())`, no signal handling (worker/scheduler *do* handle SIGINT/SIGTERM) |
| H27/H28/H29 (processor idempotency/attribution/truncation) | fixed | `processor.go` Godoc; decision table at `processor.go:460-463` |
| H19–H26 (provider contracts) | fixed | `onesignal_provider.go`, `fcm_provider.go` Godoc; interface comment in `provider.go:30-51` is **stale** (per AGENTS.md "Known landmines") |

---

## 2. System Summary (verified)

- **API server** (`cmd/server/main.go`): 7 feature modules (auth, mobile, users, staff,
  profile, segments, notifications) on one `http.ServeMux` at `:8080`; middleware chain
  = API key → staff JWT → (SUPER_ADMIN where needed) → audit; `middleware.Logging`
  outermost. No health endpoint, no TLS, no graceful shutdown.
- **Worker** (`cmd/worker/main.go`): single-goroutine `kafka-go` Reader, group
  `notification-worker-group`, topic `KAFKA_SEND_TOPIC` (default name
  `notification.send.requested`); hardcoded `NewOneSignalProvider` (line 106);
  publishes `DeliveryUpdated` to `KAFKA_UPDATE_TOPIC` (`notification.delivery.updated`).
- **Scheduler** (`cmd/scheduler/main.go`): `SCHEDULER_INTERVAL` ticker (default 30s),
  polls ≤100 due `SCHEDULED` rows, claims via `UpdateNotificationStatusIfScheduled`,
  publishes `NotificationSendRequested`; on publish failure attempts a "rollback"
  (see G19 — the rollback is ineffective).
- **State machine** (`db/migrations/000008_create_notifications.up.sql:28-38`):
  `DRAFT → SCHEDULED → QUEUED → PROCESSING → COMPLETED | FAILED` (+ unused `CANCELLED`).
  Delivery rows: `PENDING → SENT → DELIVERED | OPENED`, or `FAILED`
  (`db/migrations/000012_create_notification_deliveries.up.sql:14-23`).
- **Providers** (`internal/features/notifications`): `OneSignalProvider` (batched
  `include_player_ids` REST call, active default), `FCMProvider` (Firebase Admin SDK,
  500/chunk `SendAll`, **constructed nowhere — dead path**), `MockProvider` (test-only,
  shipped in the production package).

---

## 3. OneSignal Feature Usage & Parity Table

### 3.1 Inferred OneSignal features in use

The user stated the OneSignal feature set in use today is *unknown*; the following are
**inferred** from repo evidence only.

| # | Inferred OneSignal feature | Evidence (inferred from) |
|---|---|---|
| F-1 | Push delivery to explicit device/player IDs in one call (`include_player_ids`, batch ≤ 2,000) | `onesignal_provider.go:36` (endpoint `onesignal.com/api/v1/notifications`), `:175-179` (2,000 limit noted) |
| F-2 | Title + body content via language-keyed maps (`headings`/`contents`) | `onesignal_provider.go:495-496` — only the `"en"` key is ever set |
| F-3 | Arbitrary `data` payload shipped with the push (`notification_id`, `click_action`) | `onesignal_provider.go:497-500` |
| F-4 | High-priority delivery (`priority: 10`) | `onesignal_provider.go:202-205,501` |
| F-5 | Per-app credentials: app ID + REST API key (Basic auth) | `worker.config.go:18-27`, `onesignal_provider.go:521` |
| F-6 | Notification-ID correlation for receipts/analytics | `onesignal_provider.go:231-251` (one ID per batch), `kafka/events.go:121-124` |
| F-7 | Post-delivery analytics (delivered/opened counters) — *assumed used in OneSignal's dashboard; not confirmable from this repo* | `GetStatistics` counts `DELIVERED`/`OPENED` (`db/queries/notification_deliveries.sql` stats query; `sqlc/notifications.sql.go:213-214`); "opened"/"delivered" vocabulary in `db/queries/notification_reads.sql` and `docs/api/*.md` |

Cannot determine from the repo (→ Open questions): whether OneSignal's Web Push,
APNs/FCM receipts, deep-link/click analytics, unsubscribe/opt-out UI, quiet hours,
rich media, badges, grouping, or A/B testing were in use on the client side.

### 3.2 Parity table

| OneSignal feature (inferred) | Covered here? | Evidence | Gap |
|---|---|---|---|
| Push to explicit device tokens (F-1) | Yes, with limits | `OneSignalProvider.Send`; `FCMProvider.Send` | No chunking at the 2,000-player-ID limit in the OneSignal path (G10); FCM path not wired (G33) |
| Title/body content (F-2) | Partially | `onesignal_provider.go:495-496` — hardcoded `"en"` only | No localisation pipeline; a second language cannot be configured (G25) |
| Data payload (F-3) | Yes, with divergence | OneSignal: `notification_id` as JSON number (`:498`); FCM: same key as string (`fcm_provider.go:254`) | Clients see a different wire type per provider (G37) |
| High priority (F-4) | Yes (hardcoded) | `Priority: 10`; FCM `apns-priority: 10` / `Priority: "high"` | `notifications.priority` column (NORMAL/HIGH, `000008:15-23`) is written but never read by worker/provider (G28) |
| App credentials (F-5) | Yes | `ONESIGNAL_APP_ID`/`ONESIGNAL_API_KEY` (`worker.config.go`) | No rotation mechanism; key only reaches `worker-dev` in compose (G41) |
| Notification-ID correlation (F-6) | Yes | `DeliveryUpdated.ProviderMessageID` (`events.go:121`) | One ID per batch (H22, structural): per-device correlation only via `(ProviderMessageID, PushToken)` |
| Delivered/opened analytics (F-7) | **No** — schema exists, write path does not | `MarkNotificationDelivered`/`MarkNotificationOpened` (`db/queries/notification_deliveries.sql:63-77`) have **no callers** outside generated code (grep in `internal/features` — none found); no receipt-ingestion endpoint, no `notification_reads` API endpoint | `Delivered`/`Opened` in `GET /api/v1/notifications/{id}` statistics are permanently 0 while status is `SENT`/`FAILED` only (G21, G22) |
| User preferences / opt-out | Not covered | `users` table has only `external_id, name, email, status` (`000001`); no opt-out column found (searched `db/migrations` for opt-out/preference/quiet — none found) | Later, unless opt-out is confirmed in use (OQ-1) |
| Quiet hours | Not covered | none found (searched migrations + features for quiet/mute/snooze) | Later (no usage evidence) |
| Rich media / attachments | Not covered | `OneSignalRequest` has no attachment fields; Godoc states unsupported (`onesignal_provider.go:154-159`) | Later (no usage evidence) |
| A/B testing, grouping/collapse, badges | Not covered | none found | Later (no usage evidence) |
| Upload-based audiences (OneSignal "custom audience file") | Not covered, schema half-built | `upload_batches`/`upload_batch_rows` tables exist (`000009`, `000010`) but `expandTargets` has `case "UPLOAD": continue // TODO` (`processor.go:659-661`) | UPLOAD-target notifications silently complete with 0 deliveries (G15) |

---

## 4. Gap Analysis

### A. Production readiness

| ID | Gap | Evidence | Impact | Severity |
|---|---|---|---|---|
| G1 | **No CI, no linter, no formatter config.** None found (searched repo root for `.github/`, `*.golangci*`, `lint` configs — none found; AGENTS.md "No CI, no linter, no formatter config"). | `AGENTS.md` §Commands; repo root listing | Regressions land on `main` undetected; `main` already carries a RED test suite (G2) | High |
| G2 | **Test suite RED on main; stale committed mocks.** Per user (taken as given): `internal/features/staff` build error (`service_test.go:488` arg count), `internal/features/segments` stale `mocks/mocks.go` (missing `CountSearch`, `CountBySegmentIDs`). Unverified (needs execution). | `AGENTS.md` §Commands; 6 `*_test.go` files exist (`internal/features/{staff,segments,notifications}`, `internal/middleware`, `internal/token`) | Any change touching staff/segments is unverifiable; mocks are committed, so `go tool mockery` must be run after interface changes | Blocker |
| G3 | **No health/readiness/liveness endpoint on any binary.** Searched `*.go` for `health|readiness|liveness|/ready` — none found (only an unrelated comment in `internal/database/repository/roles.go:36`). | `cmd/server/main.go` (single route mux), `cmd/worker/main.go`, `cmd/scheduler/main.go` | Orchestrators/probes cannot detect an up-but-dead process; DB outages invisible until user impact | High |
| G4 | **No production deployment path.** Only `Dockerfile.dev` (Air hot-reload, dev-only) exists; README references a `docker-compose.prod.yml` that is not in the repo (searched root — not found). No TLS (`http.Server{Addr: ":8080"}`, `cmd/server/main.go:156-162`). Server has no graceful shutdown (H43, still open on `main`). | `docker-compose.yml` (all services are `-dev` + Air), `Dockerfile.dev` | The system cannot be deployed to production as-is; every deploy is a hand-built artifact | High |
| G5 | **Manual Kafka topic provisioning; partitioning/retention unplanned.** `AllowAutoTopicCreation: false` (`internal/kafka/producer.go:91`) so both topics (`notification.send.requested`, `notification.delivery.updated`) must be created by hand; no init script in compose; partition count, replication factor, and retention are decided ad hoc. Dev cluster runs RF=1 (`docker-compose.yml:34-36`). | `internal/kafka/producer.go:49-68`, `docker-compose.yml`, AGENTS.md §Local environment gotchas | Worker throughput is capped at partition count (consumer group); a wrong/missing topic fails loudly or silently stalls; known gap, confirmed | High |
| G6 | **Secrets handling.** All secrets are plaintext env vars; `godotenv.Load()` runs in every production binary (`base.config.go:118`); `docker-compose.yml` injects `API_KEY`, `ACCESS_SECRET`, `ONESIGNAL_API_KEY` etc. from a host `.env`; no rotation mechanism anywhere (searched for `rotat` — none found). OneSignal key reaches only `worker-dev` (AGENTS.md note, confirmed in `docker-compose.yml:131-132`). | `internal/config/base.config.go`, `.env.example`, `docker-compose.yml` | A leaked `.env` compromises admin API, JWT minting, and push sending simultaneously; rotation requires re-deploy of every binary | Medium |
| G7 | **DB DSN built without percent-encoding** (L7) — non-alphanumeric passwords break all four binaries at startup. | `base.config.go:183-193` | Operational foot-gun; low likelihood, total outage when it hits | Low |

### B. Reliability and resilience

| ID | Gap | Evidence | Impact | Severity |
|---|---|---|---|---|
| G8 | **Worker drops a send request on any processor failure.** The consume loop logs the error and `continue`s *without* committing or seeking back (`cmd/worker/main.go:146-149`). With `kafka-go`'s `Reader` there is no redelivery of that message: a transient DB blip or provider timeout during `ProcessSendRequested` permanently loses the `NotificationSendRequested` event, and the notification sits in `QUEUED`/`PROCESSING` forever. "At-least-once" holds only across *process restarts*, not across in-process errors. | `cmd/worker/main.go:131-155`, `processor.go` error returns | A single transient error strands a campaign with no operator-visible signal (ties to G21) | Blocker |
| G9 | **Scheduler's publish-failure "rollback" is a no-op.** After claiming `SCHEDULED → QUEUED`, a failed Kafka publish calls `UpdateStatusIfScheduled(ctx, id, "SCHEDULED")` — but that query only updates when `status = 'SCHEDULED'` (`db/queries/notifications.sql:87-93`). The row is `QUEUED`, so `rowsAffected = 0`; the notification is stranded: the scheduler only polls `SCHEDULED` rows and the worker only reacts to the Kafka event that was never published. | `cmd/scheduler/main.go:176-181`, `db/queries/notifications.sql:87-93` | Every scheduled notification whose publish failed (broker blip, topic misconfig) is silently lost | High |
| G10 | **OneSignal batch not chunked at the 2,000 player-ID limit.** `OneSignalProvider.Send` sends all valid tokens in one request; >2,000 ⇒ OneSignal 400 ⇒ *entire campaign* FAILED. `FCMProvider` chunks at 500 (`fcm_provider.go:26`) — the asymmetry is only meaningful once FCM is wired (G44). | `onesignal_provider.go:175-179, 437+`, `processor.go` (no chunking around `provider.Send`) | The largest campaigns (broadcasts) are the ones that cannot be delivered at all | High |
| G11 | **No provider retry, error classification, or circuit breaker.** Exactly one `provider.Send` call per notification; provider errors are unclassifiable (`provider.go:196-201`: "callers cannot use errors.Is to distinguish failure modes"); no backoff/TTL/DLQ (searched `*.go` for `circuit|breaker|backoff|exponential` — none found). | `processor.go:375-384`, `provider.go` | A 30-second OneSignal outage marks every in-flight campaign FAILED; no recovery without manual re-send (which is blocked by the DRAFT-only rule, `service.go:1015`) | High |
| G12 | **No transactional outbox for DB→Kafka handoff.** `Send` claims `DRAFT→QUEUED` then publishes; a crash *between* the commit and the publish leaves a `QUEUED` row with no event and no reaper (scheduler only handles `SCHEDULED`; worker only handles events). The rollback covers *publish errors*, not crashes. Same pattern in scheduler (G9). | `service.go:1019-1037` + its Godoc ("A transactional outbox is required to close that cross-system delivery gap"), `processor.go:242-250` (same admission on the update topic) | Lost campaigns require manual DB intervention; the cross-system gap is acknowledged in code comments but unfixed | High |
| G13 | **No dead-letter / quarantine for poison messages.** Undecodable events are committed and dropped with a log line (`cmd/worker/main.go:140-144`); no DLQ topic, no per-attempt cap, no operator tooling to inspect them (searched `dlq|dead[- ]?letter` — none found). | `cmd/worker/main.go:139-144` | Schema-evolution bugs or corrupted events vanish; investigation = log archaeology | Medium |
| G14 | **One-shot in-memory audience expansion, 1,000,000-row cap.** `expandTargets` materialises up to 1M users/members per campaign into a slice; a full page is logged but still delivered as a truncated audience (`processor.go:25-40, 605-663`). | `processor.go` (`audienceFetchLimit`), H29 | Broadcasts above the cap silently under-deliver; memory spikes proportional to audience | Medium |
| G15 | **UPLOAD targets silently ignored** (see F table; processor `case "UPLOAD": continue // TODO`, `processor.go:659-661`). Tables `upload_batches`/`upload_batch_rows` exist (`000009`, `000010`) but no API exposes them (none of the 7 modules registers `/upload*` routes — searched `routes.go` files). | `processor.go:659-661`, `db/migrations/000009*.sql` | A campaign whose audience is an upload completes with 0 deliveries and status `COMPLETED` | Medium |
| G16 | **Partial-fan-out loss window is permanent.** A crash after *any* delivery insert but before `COMPLETED` leaves `PROCESSING` with rows > 0; the idempotency guard then skips the notification forever — the unrecorded remainder is lost by design (documented in `processor.go:93-102, 210-214`). Also a crash between the last insert and the `COMPLETED` update leaves the row `PROCESSING` forever with no reaper. | `processor.go` Godoc "Delivery-row atomicity" / "Idempotency check" | Metrics undercount and some devices never get the notification; no recovery path | Medium |
| G17 | **OneSignal partial acceptance fails the whole batch and the whole campaign.** `recipients < submitted` ⇒ all tokens `FAILED` ⇒ notification `FAILED` (`onesignal_provider.go:559-573` + `processor.go:375-384`). Documented as deliberate (H23 over-delivery-on-retry rationale), but there is no per-token retry to *reduce* the loss once a campaign is marked FAILED. | `onesignal_provider.go:559-573`, `processor.go:375-384` | One bad player ID in a 50k batch can kill the campaign (until G11/G12 land) | Low |
| G18 | **Unattributable delivery ⇒ FK violation ⇒ record and event both lost.** When a result's token matches no input token, `deviceTokenID` stays 0 and `Create` runs against a `NOT NULL` FK (`000012:49-52`); the insert fails, the row is skipped, *and* the `continue` also skips the `DeliveryUpdated` publish (`processor.go:390-427`). | `processor.go:391-421`, `db/migrations/000012_create_notification_deliveries.up.sql:8,49-52` | Silent loss of delivery evidence exactly when a provider misbehaves (diagnostic value is highest there) | Low |

### C. Observability

| ID | Gap | Evidence | Impact | Severity |
|---|---|---|---|---|
| G19 | **No metrics, no distributed tracing, no alerting.** M38, confirmed: `go.mod` has zero direct metrics/OTel dependencies (OTel entries are transitive via Firebase, `go.mod:82-90`); all output is `log.Printf`. | `go.mod`, `AGENTS.md` (M38) | No SLOs, no alerting on worker death/lag, no throughput or failure-rate visibility — the primary risk of a push system is silent under-delivery | High |
| G20 | **Unstructured logging; no request ID; no panic recovery.** `middleware/logger.go` uses `fmt.Println` and logs method+path only (M35/M36); no recover wrapper (M37); worker logs are unstructured lines. | `internal/middleware/logger.go` (M35-M37), `cmd/worker/main.go` log lines | Requests cannot be correlated across middleware → handler → Kafka → worker; a handler panic reaches `net/http`'s default recovery | Medium |
| G21 | **No Kafka or provider visibility.** No consumer-lag, queue-depth, provider-latency, or batch-size signal anywhere (none found). | `cmd/worker/main.go`, `cmd/scheduler/main.go` | G8/G9-style strandings have zero observable symptom until a user complains | Medium |

### D. Data lifecycle

| ID | Gap | Evidence | Impact | Severity |
|---|---|---|---|---|
| G22 | **No retention, partitioning, or archival.** `notification_deliveries` grows by (devices × campaigns) with only 4 B-tree indexes (`000012:55-65`); `audit_logs` grows monotonically (`000014`); the statistics query scans the delivery table per `GetByID` (`GetStatistics`, `sqlc/notifications.sql.go:213-214`); no partitioning or TTL migration exists (searched `db/migrations` for `partition|ttl|archive` — none found). | `db/migrations/000012*.sql`, `db/queries/notification_deliveries.sql` | After a few months of campaign history, list/statistics queries and the fan-out inserts degrade; no way to bound storage cost | High |
| G23 | **Delivery history cascade-deleted with the device/user.** `ON DELETE CASCADE` on `device_tokens` and `users` FKs wipes analytics rows when a user unregisters or a token is deleted (`000012:39-52`; `DELETE /api/v1/device-tokens/{id}` is a live route, `users/routes.go:76`). | `db/migrations/000012*.sql`, `internal/features/users/routes.go` | Deleting a device token destroys the per-device delivery audit trail | Low |
| G24 | **No backup/restore automation, no staging.** No backup tooling, WAL configuration, or restore runbook anywhere in the repo (searched `README.md`, `docs/`, `Makefile` for `backup|restore|pg_dump` — none found; README's "Production Checklist" lists it as an unchecked task). `make reset` is `docker compose down -v` (data loss by design, `Makefile:69-73`). | `Makefile:69-73`, `README.md` "Production Checklist" | RPO is unknown; there is no environment between dev and prod to validate a migration | Medium |

### E. API design consistency

| ID | Gap | Evidence | Impact | Severity |
|---|---|---|---|---|
| G25 | **Error→HTTP mapping still divergent; 3 known 500 `err.Error()` leaks.** H1–H8 were largely addressed in the recent pass (AGENTS.md), but the documented hold-outs remain in `members_handler.go` (lines 108, 195, 258 per AGENTS.md; Unverified — not re-confirmed line by line this session). Cross-feature: sentinel names mislead (`ErrCannotDeleteSent` actually means "not DRAFT", `service.go:104-113`), and repository failures still collapse to not-found in some paths (H5–H8 patterns; `notifications` service docs admit it, `service.go:44-50`). | `AGENTS.md` §Known landmines, `internal/features/segments/members_handler.go`, `internal/features/notifications/service.go` | Clients get wrong status codes for wrong reasons; DB outages surface as 404s | Medium |
| G26 | **Status vocabulary drift across four layers.** `model.go` constants (`DRAFT, SCHEDULED, SENDING, SENT, FAILED`, M15) vs raw strings written by code (`QUEUED, PROCESSING, COMPLETED`) vs the DB CHECK constraint vs the generated `MarkNotificationSent` query, which writes `status = 'SENT'` — a value the CHECK constraint in `000008:28-38` **forbids** (would fail at runtime if ever called; no caller found, grep in `internal/features` — none found). | `internal/features/notifications/model.go`, `db/queries/notifications.sql:111-114`, `db/migrations/000008_create_notifications.up.sql:28-38` | A latent landmine: any future caller of `MarkNotificationSent` hits a CHECK violation; statuses cannot be reasoned about from any single source | Medium |
| G27 | **DTO/helper duplication and weak PATCH semantics.** M1–M8 (five copies of staff-ID extraction, three `Pagination` structs, duplicate DTOs), M24–M26 (plain-string update DTOs: cannot clear a field, cannot distinguish omitted `false`), silent pagination clamping (cross-cutting §6). | M1–M8, M24–M26 in `code-review.md`; e.g. `staff/dto.go:UpdateStatusRequest` | Every fix must be applied in N places; API cannot express real partial updates | Low |
| G28 | **Campaign statistics are wrong by construction.** `GET /api/v1/notifications/{id}` reports `Delivered`/`Opened`, but the counters only count rows in `DELIVERED`/`OPENED` state (`sqlc/notifications.sql.go:213-214`) and nothing in the system ever transitions a delivery into those states (see G29). While the provider reports `SENT`, the admin portal shows 0 delivered. | `sqlc/notifications.sql.go:213-214`, `processor.go:406-417` (writes only `SENT`/`FAILED`), `service.go:463-473` | The headline analytics number on the campaign detail page is fabricated-zero; operators cannot trust any readout | High |

### F. Product features

Only gaps tied to an inferred OneSignal feature (F-1…F-7) or a stated requirement count; everything else is marked **Later**.

| ID | Gap | Evidence | Impact | Severity |
|---|---|---|---|---|
| G29 | **No delivery-receipt ingestion path (ties to F-6/F-7).** `MarkNotificationDelivered`, `MarkNotificationOpened`, `MarkNotificationFailed` and the `notification_reads` queries exist in `db/queries` but have **no callers** outside generated code (grep in `internal/features` — none found); no HTTP endpoint accepts provider receipts or in-app "opened" reports; `notification_deliveries.retry_count`/`failed_reason`/`sent_at` are never written by the processor. | `db/queries/notification_deliveries.sql:63-85`, `db/queries/notification_reads.sql`, `processor.go:410-417` | DELIVERED/OPENED are unreachable states; engagement analytics (a stated OneSignal use) cannot be reproduced in-house | High |
| G30 | **No localisation pipeline (ties to F-2).** Content is single-language; `OneSignalRequest.Headings/Contents` maps are schema placeholders only, always `{"en": …}` (`onesignal_provider.go:182-188, 495-496`); `notifications` schema has one `title`/`body` pair, no per-locale content. | `onesignal_provider.go:182-188`, `db/migrations/000008*.sql` | Multi-language audiences get English only; parity with OneSignal's content maps is superficial | Medium |
| G31 | **`click_action` hardcoded in the worker** (`processor.go:365`, `ClickAction: "NOTIFICATION_CLICK"`), even though H26 explicitly made it provider-callable to remove client assumptions from the server. | `processor.go:360-366`, `provider.go:138-146` | One implicit client contract baked into the worker; changing the routing token is a server deploy | Low |
| G32 | **`notifications.priority` is a dead knob.** Column exists (`NORMAL`/`HIGH`, `000008:15-23`), nothing reads it: both providers hardcode high priority (`onesignal_provider.go:501`, `fcm_provider.go:271-278`). | `db/migrations/000008*.sql`, both providers | API surface advertises a control that does nothing | Low |
| G33 | **UPLOAD audience half-implemented** (see G15; schema present, processor no-op, no upload API). | `processor.go:659-661`, `000009`/`000010` | Announced capability that silently misfires | Medium |
| G34 | **Later (no OneSignal-usage evidence found, per ground rule):** user preferences/opt-out (no column/table found — searched migrations), quiet hours (none found), rich media/attachments (unsupported per `onesignal_provider.go:154-159`), A/B testing (none found), notification grouping/collapse (none found), badges (none found), Web Push (`WEB` appears only as a `platform` CHECK value, `000002:13-18`; no web-token dispatch path). | none found (searched `db/migrations`, `internal/features`) | Deferred; re-open if OQ-1 confirms any of these were in OneSignal use | — (Later) |

### G. Security

| ID | Gap | Evidence | Impact | Severity |
|---|---|---|---|---|
| G35 | **No rate limiting anywhere** (M45). Includes `/api/v1/auth/login` and the shared-API-key surface. | `code-review.md` M45; no limiter in `internal/middleware` (file listing: api_key, audit, jwt, logger, mobile_jwt, super_admin, middleware) | Brute-force and abuse are unbounded once the service is exposed | Medium |
| G36 | **Mobile-JWT trust boundary is wide.** `MOBILE_JWT_SECRET` is a shared HMAC secret with an external Laravel system; `iss`/`aud` are not enforced (documented: `mobile/claims.go:64-71`); the token travels in the JSON body. Staff-side JWTs also skip `iss/aud/sub` enforcement (M40–M41). | `internal/features/mobile/claims.go`, `code-review.md` M40–M41, M47 | Any system that shares the secret and mints a compatible shape can register/delete device tokens for any user | Medium |
| G37 | **PII + transport posture.** `DB_SSLMODE=disable` in the example config; no TLS on the API (G4); user `name`/`email` and push tokens stored in plaintext columns; audit middleware trusts `X-Forwarded-For` unconditionally (C5, open). | `.env.example:6`, `000001`, `internal/middleware/audit.go` (C5) | PII at rest is protected only by Postgres access control; audit IP may be spoofed behind a proxy | Medium |
| G38 | **No secret-rotation mechanism.** JWT secrets, API key, and OneSignal REST key are static env vars; rotation = coordinated re-deploy of all four binaries; no dual-key or grace window anywhere (none found). | `internal/config/*.config.go` | A suspected-leaked secret forces a full outage or a risky stop-the-world rotation | Low |
| — | Residual items from C6/C7/C14–C16, C18–C19: not re-verified this session; assumed in the same "point-fix" state as the items in §1. Re-verify at SPEC time. | `code-review.md` | (no independent severity) | — |

### H. Testing and quality

| ID | Gap | Evidence | Impact | Severity |
|---|---|---|---|---|
| G39 | **Thin, RED test base.** 6 test files across 5 packages (`staff`, `segments`, `notifications` ×2, `middleware`, `token`); no test touches the Kafka boundary, HTTP handlers, or the processor's provider contract beyond unit tests of `Processor`/`MockProvider`. Suite is RED on main (G2). `MockProvider` ships in the production package (AGENTS.md "Known landmines"). No contract tests pinning the provider order/length invariant the processor depends on (`processor.go:232-240`). | `glob **/*_test.go` (6 files), AGENTS.md | Regression safety is near-zero for exactly the code paths (Kafka loop, fan-out, attribution) where G8–G18 live | High |
| G40 | **Stale interface documentation.** `provider.go:30-51` still describes the *pre*-H19..H26 provider behaviour (shorter slices, `nil, err` batch failure) and is explicitly flagged untrustworthy in AGENTS.md. | `internal/features/notifications/provider.go`, AGENTS.md §Known landmines | Implementer agents will write code to the stale contract if they read the interface Godoc | Low |

### I. Operations

| ID | Gap | Evidence | Impact | Severity |
|---|---|---|---|---|
| G41 | **README is stale and internally contradictory.** Claims "no automated tests" (6 test files exist), points to `internal/config/config.go` (split into 5 files, AGENTS.md), lists a migration/structure tree that no longer matches `git ls-files`, describes the worker as "Calls OneSignal API **for each device**" (`README.md:408` — it is one batched call), and its "Key Files" paths do not exist. L8/L9 partially. | `README.md:13,84,222-227,408,651-654` vs AGENTS.md | New team members and implementation agents inherit wrong mental models | Medium |
| G42 | **No runbook, DR procedure, or operator tooling.** Failure-mode runbooks (stranded `QUEUED`/`PROCESSING` rows from G8/G9/G12/G16, topic missing from G5, poison messages from G13) would today be improvised SQL; no DBA/operator commands documented beyond `Makefile` dev targets. | `README.md` "Troubleshooting" (4 rows, all dev-local), `Makefile` | Mean-time-to-recover for production incidents is unbounded | Medium |
| G43 | **Untunable DB pool; single-node Kafka in compose.** `database.New` exposes no pool sizing (L5); dev cluster is 1 broker, RF=1 (`docker-compose.yml:34-36`). | `internal/database/db.go` (L5), `docker-compose.yml` | Pool starvation is unconfigurable; local behaviour understates production failure modes | Low |

### J. Migration from OneSignal

Context: the worker **still dispatches through OneSignal** (`cmd/worker/main.go:106`, hardcoded `NewOneSignalProvider`). `FCMProvider` exists but is unreachable — `NewFCMProvider` has no callers (grep in `cmd/` and `internal/` — none found). The migration therefore has four real gaps, not one.

| ID | Gap | Evidence | Impact | Severity |
|---|---|---|---|---|
| G44 | **Provider selection is not a config decision.** The worker hardcodes OneSignal; AGENTS.md confirms "swapping providers is a code edit". `FCMProvider` (incl. its credentials handling) is dead code. No config key selects the provider; FCM credentials have no env keys at all (`.env.example` has only `ONESIGNAL_*`). | `cmd/worker/main.go:105-106`, `.env.example`, AGENTS.md §Known landmines | Cutover requires a code change + redeploy; A/B or shadow comparison is impossible | High |
| G45 | **No FCM/APNs credential path.** `NewFCMProvider` takes a service-account JSON byte slice (`fcm_provider.go:100`); nothing in config, compose, or docs wires it; no secret-store integration exists (G6). | `fcm_provider.go:100-128`, `internal/config/worker.config.go` | The alternative provider cannot even be constructed in any environment | High |
| G46 | **Token/identifier migration problem is unsolved.** `device_tokens.push_token` holds whatever the client registered; API docs call it "FCM or APNS token" (`docs/api/users-api.md:274,300`) while the live provider sends it to OneSignal as a player ID. There is no mapping table between OneSignal player IDs and direct FCM/APNS tokens, no import tool, no identifier-migration migration; `uq_device_push_token` is a global unique index (`000002:53-54`). Whether stored tokens are already portable FCM tokens or OneSignal-internal player IDs is **undeterminable from the repo** (OQ-7). | `docs/api/users-api.md:274`, `onesignal_provider.go:445-446` (token sent verbatim), `db/migrations/000002*.sql` | If OneSignal player IDs ≠ FCM tokens, cutover requires re-tokenization of every device via the client app — the single largest migration cost | High |
| G47 | **No shadow-run / dual-send / delivery-comparison capability.** There is no mechanism to dispatch the same campaign to both providers (or to OneSignal while the in-house panel is in "dry-run" mode) and diff outcomes; the processor records to one delivery table with a hardcoded `Provider: "ONESIGNAL"` string (`processor.go:414`). | `processor.go:414`, no `shadow|parallel|canary` found (searched `*.go` — none found) | Cutover risk is unmeasured; a bad migration is only discovered post-facto via user complaints | High |
| G48 | **Client payload divergence between the two providers.** `notification_id` is a JSON number via OneSignal (`onesignal_provider.go:498`) but a string via FCM (`fcm_provider.go:254`); `click_action` is always present via OneSignal but omitted when empty via FCM (`fcm_provider.go:260-262`); Android channel ID is declared but never set on the OneSignal side (`onesignal_provider.go:196-200`). During any dual-run, clients see two payload dialects. | `onesignal_provider.go:497-501` vs `fcm_provider.go:253-279` | Client-side parsing must tolerate both dialects for the entire shadow period; divergence bugs are user-visible | Medium |
| G49 | **No rollback plan.** OneSignal remains the live provider, so rollback *today* is trivial — but nothing documents how long the OneSignal app stays funded/enabled, that the client may still hold OneSignal-issued tokens, or what "rollback" means after token re-registration (G46). No rollback section exists in any doc (searched `docs/` for `rollback` — none found outside this assessment). | `docs/` (none found), `cmd/worker/main.go:106` | Cutover decisions cannot be made reversibly; a failed FCM migration has no defined undo | Medium |

---

## 5. Prioritized Roadmap

Effort: S ≤ ~1 week · M ≈ 1–3 weeks · L > 3 weeks (single implementer-agent, one item at a time).
Dependencies use ADR IDs from §6 (to be written one at a time in Phase 2).

### Phase 0 — Blockers (nothing else is safe without these)

| # | Item | Findings | Effort | Depends on |
|---|---|---|---|---|
| 0.1 | Worker consumption error handling: bounded in-memory retries, redelivery/requeue, poison-message quarantine (DLQ topic or committed-and-recorded table). Rewrite the consume loop in `cmd/worker/main.go` (kafka-go `Reader` semantics must be addressed in the ADR). | G8, G13 | M | — |
| 0.2 | Fix the scheduler rollback (use `UpdateNotificationStatusIfQueued` for `QUEUED → SCHEDULED`, or fold into the outbox work 0.4). | G9 | S | — |
| 0.3 | OneSignal chunking at 2,000 player IDs (chunk in `OneSignalProvider.Send`; keep per-token result ordering contract). | G10 | S | — |
| 0.4 | Get the test suite green: regenerate mocks (`go tool mockery`), fix `staff` test signature, fix `segments` batch-count assertions. No design decision needed; SPEC only. | G2, G40 | S | — |
| 0.5 | Minimal CI: `go build ./...`, `go test ./...`, `gofmt` check, vet; enforce before merge (commit-to-main policy stays, CI gates the commit). | G1 | M | 0.4 |
| 0.6 | Config-driven provider selection + FCM credential plumbing (new env keys, provider factory in `cmd/worker`, no behaviour change when `ONESIGNAL` is selected). | G44, G45 | M | — |

### Phase 1 — Reliability (durable, no lost campaigns)

| # | Item | Findings | Effort | Depends on |
|---|---|---|---|---|
| 1.1 | Provider retry & failure handling: classified errors, bounded exponential backoff with TTL, terminal FAILED, circuit breaker, `retry_count`/`failed_reason` actually written. **ADR-01 draft already exists** (`docs/adr/bigpickle-retry.md`, unreviewed) — review/replace it first. | G11, G16, G17, G18 | L | 0.1, 0.6 |
| 1.2 | Transactional outbox for `QUEUED → Kafka` (server `Send` + scheduler), closing crash-window stranding; scheduler's publish path routes through the same outbox poller or reaper. | G12, G9 (residual) | L | 0.2 |
| 1.3 | Delivery receipt ingestion: receipt source chosen in ADR-05 (provider receipts and/or in-app "opened" endpoint), write path calling the existing `MarkNotificationDelivered`/`MarkNotificationOpened`/`notification_reads` queries, statistics become truthful. | G29, G28 | M | 0.6 |
| 1.4 | Health/readiness endpoints on all four binaries (DB ping, Kafka broker ping, worker consumer-lag probe). | G3 | S | — |
| 1.5 | Server graceful shutdown (H43, still open) + producer `Close` wiring (`module.go` producer never closed — documented, acceptable only at process exit). | H43, H44 | S | — |
| 1.6 | Point fixes: attribution-mismatch handling (G18), `click_action` from config/row instead of worker constant (G31), decide fate of `priority` column (G32), retire or align `MarkNotificationSent` with the CHECK constraint (G26). | G18, G31, G32, G26 | S | — |

### Phase 2 — Production hardening (observable, deployable, bounded)

| # | Item | Findings | Effort | Depends on |
|---|---|---|---|---|
| 2.1 | Observability baseline: structured logging + request ID + panic recovery in middleware; Prometheus metrics (request rate/latency, worker lag, provider batch outcomes, campaign state counts); alert rules for G8/G9/G12-style stranding and worker death. | G19, G20, G21 | M | 1.1, 1.4 |
| 2.2 | Kafka operations baseline: topic-provisioning script (names, partition count, retention, RF), documented in runbook; consumer-group lag monitoring. | G5 | S | 2.1 |
| 2.3 | Production deployment path: real images (no Air), TLS termination decision, compose/K8s prod manifest, secrets from a store instead of `.env`. | G4, G6 | M | 2.1 |
| 2.4 | Security hardening: rate limiting (login + API-key surface), body-size/Content-Type limits, mobile-JWT `iss`/`aud` enforcement, XFF trust rules. | G35, G36, G37 | M | 2.3 |
| 2.5 | Data retention & archival: policy + partitioning or archive job for `notification_deliveries` and `audit_logs`; statistics queries switched to archived-aware reads. | G22, G23 | L | 2.1 |
| 2.6 | Ops: backup/restore procedure + tested restore, staging environment, incident runbooks (stranded campaigns, missing topic, poison messages, provider outage). | G24, G42 | M | 2.2, 2.3 |
| 2.7 | API consistency pass: central error mapper, status-constant reconciliation, PATCH semantics, DTO dedup. | G25, G26, G27 | M | 0.5 |

### Phase 3 — OneSignal cutover (after reliability, because shadow run needs trustworthy measurement)

| # | Item | Findings | Effort | Depends on |
|---|---|---|---|---|
| 3.1 | Token/identifier migration: determine token format reality (OQ-7), build the mapping (or re-registration) tooling, migration migration-pair + backfill job, client re-token SDK change if needed. | G46 | L | 0.6, 1.3, 2.1 |
| 3.2 | Shadow run & delivery comparison: dual-send mode (OneSignal live + FCM shadow, or vice-versa), per-device outcome diff, parity report; cutover gate defined from the report. | G47, G48 | L | 1.1, 3.1 |
| 3.3 | Rollback runbook + cutover go/no-go checklist; contract that OneSignal app remains enabled for N days post-cutover. | G49 | S | 3.2 |

### Phase 4 — Product parity & scale (sequenced after OQ-1 answers)

| # | Item | Findings | Effort | Depends on |
|---|---|---|---|---|
| 4.1 | Localisation pipeline (per-locale title/body; locale from user profile). | G30 | M | 1.3 (schema) |
| 4.2 | UPLOAD audience end-to-end (API + worker). | G15, G33 | L | 1.1 |
| 4.3 | Streaming audience expansion (replace 1M in-memory cap). | G14 | M | 4.2 |
| 4.4 | Preferences/opt-out, quiet hours, rich media, badges, grouping, A/B — **Later**, only if OQ-1 confirms OneSignal usage. | G34 | — | OQ-1 |

---

## 6. ADR / SPEC Inventory (to write next — contents not yet written)

Sequencing note: every item below becomes exactly one ADR (for the team) paired with one SPEC
(for implementer + tester), reviewed later against `docs/reviews/<feature>.md`. ADR-01 has an
unreviewed draft already; treat it as input, not as decision.

| ID | Title | Rationale (findings) |
|---|---|---|
| ADR-01 | Retry & failure handling for provider delivery (classified errors, backoff, TTL, circuit breaker, durable retry ledger) | G11, G16, G17, G18. Draft exists at `docs/adr/bigpickle-retry.md` (unreviewed, not authoritative) |
| ADR-02 | Worker consumption semantics: bounded retries, redelivery, and dead-lettering (kafka-go `Reader` constraints, DLQ topic payload, offset policy) | G8, G13 |
| ADR-03 | Durable `QUEUED → Kafka` handoff: transactional outbox + reaper, incl. scheduler publish path and the broken rollback fix | G9, G12 |
| ADR-04 | Config-driven provider selection and FCM/APNs credential plumbing (new env keys, provider factory, which binary loads what) | G44, G45 |
| ADR-05 | Delivery receipt ingestion: source of DELIVERED/OPENED (provider receipts vs in-app report), new endpoints, write path onto existing `Mark*` queries, truthful statistics | G28, G29 |
| ADR-06 | OneSignal → direct-FCM cutover: token/identifier migration, shadow-run comparison design, cutover/rollback contract, client payload unification | G46, G47, G48, G49 |
| ADR-07 | Kafka topology baseline: topic names/partitions/retention/RF, provisioning-as-code, consumer-group policy | G5, G43 |
| ADR-08 | Observability baseline: health/readiness endpoints, structured logging + request ID, metrics catalog, alert rules | G3, G19, G20, G21 |
| ADR-09 | API consistency conventions: central error mapper, status vocabulary reconciliation, real PATCH semantics, pagination policy | G25, G26, G27 |
| ADR-10 | Data retention & archival for delivery and audit data (policy, partitioning vs archive job, PII deletion semantics) | G22, G23, G24 |
| ADR-11 | Security hardening baseline: rate limiting, TLS posture, JWT claim enforcement, body limits, secret rotation | G6, G35, G36, G37, G38 |
| ADR-12 | (Later, conditional on OQ-1) Product parity scope: localisation first, then opt-out/quiet-hours/rich-media if OneSignal usage is confirmed | G30, G34 |

Each ADR gets a paired SPEC with: exact file paths/symbols to touch, new-file full paths, migration
filenames (6-digit prefix, `.up.sql` + `.down.sql`) and schema changes, new config keys + owning
binary, new Kafka topics/events + payload shape, `go tool mockery` regeneration call-outs where
repository interfaces change, and a closing Acceptance-cases table.

---

## 7. Open Questions (for stakeholders)

- **OQ-1.** Which OneSignal features does the client actually use today? Specifically: OneSignal
  SDK in the app (vs direct FCM), Web Push, opt-out/preferences UI, quiet hours, rich media,
  badges, notification grouping, A/B campaigns, click analytics. (Drives G34/ADR-12.)
- **OQ-2.** Expected scale: total active devices, campaigns/day, largest broadcast, sustained
  rate. The ADR-01 draft assumes 100 notifications/s sustained with 1,000/s bursts — is that the
  actual requirement? (Drives G14, ADR-07 partition sizing, Phase 4.)
- **OQ-3.** Target end-state: full deprecation of OneSignal (direct FCM/APNs only), or keep the
  OneSignal provider as a configurable fallback? (Drives ADR-04 scope.)
- **OQ-4.** Where do delivery receipts come from post-migration? FCM v1 receipts, APNs feedback,
  or in-app "opened" reporting from the mobile client? Is there an existing analytics consumer of
  `notification.delivery.updated` we must keep compatible? (Drives ADR-05.)
- **OQ-5.** Production infrastructure: what terminates TLS? Is a single-node Kafka acceptable in
  prod? Managed or self-hosted Postgres? Does a staging environment exist at all? (Drives ADR-07/08/11.)
- **OQ-6.** Legal/retention requirements for delivery history, audit logs, and user PII (email,
  name, push tokens)? Any mandated deletion window? (Drives ADR-10.)
- **OQ-7.** What is actually stored in `device_tokens.push_token` right now — OneSignal player
  IDs or FCM/APNS registration tokens? Do the mobile clients currently talk to OneSignal or to
  FCM? (Determines whether cutover needs client re-tokenization; the repo is ambiguous: docs say
  "FCM or APNS token", the live provider treats them as OneSignal player IDs.)
- **OQ-8.** `MOBILE_JWT_SECRET` is shared with the Laravel backend — who owns rotation, and what
  is the acceptable blast radius of a mobile-token forgery? (Drives ADR-11.)
- **OQ-9.** Is UPLOAD-audience support a real requirement (tables already exist, `000009`/`000010`)
  or abandoned schema? (Drives Phase 4.2.)
- **OQ-10.** Roadmap sign-off: which phases are in scope for the coming quarter, and is the
  Blocker/High severity assignment contested for any item (especially G8, G9, G28)?

---

*End of Phase 1 assessment. No ADRs or SPECs were written in this session.*
