# Notification Panel Backend — Code Review Findings

**Version:** 1.7

**Last Updated:** 2026-11-03

**Scope:** Full codebase audit conducted during the Godoc documentation pass  
**Coverage:** `cmd/`, `internal/`, `pkg/` — every non-test, non-generated `.go` file

---

## Overview

This is a live inventory of bugs, security gaps, correctness issues, and design weaknesses. The original audit was drawn from the Godoc pass; later targeted reviews verify implementation and pinned dependency behavior directly. Older rows/checklists are not a reconciled list of still-open defects. Findings H47/H48/M48 remain open. C21 was remediated and locally verified in M02a on 2026-11-02; see its row/evidence. Proposed ADRs alone do not fix runtime behavior.

The findings are grouped by severity rather than by file, so that remediation can be prioritised without having to open every source file. A cross-cutting section follows the itemised lists and identifies patterns that recur across multiple features.

Dependency baseline update (2026-11-02): step 18's govulncheck found 24 reachable
advisories initially. Go/module security updates and a final scan reduced the
reachable result to zero; uncalled dependency advisories remain informational.
See [CI evidence](docs/analysis/ci-readiness.md) for versions and limitations.
The dependency scan does not close runtime findings; C21 is separately remediated by M02a code/tests. H47/H48/M48 and unreconciled older rows remain separate.

---

## Severity Legend

| Level | Meaning | Action |
|-------|---------|--------|
| **CRITICAL** | Crashes, data loss, security bypass, or corruption visible to end users | Fix immediately, before any new feature work |
| **HIGH** | User-visible bugs or security weaknesses that do not corrupt data | Fix within the current cycle |
| **MEDIUM** | Design inconsistencies, maintainability hazards, contract drift | Schedule for a dedicated refactor pass |
| **LOW** | Style, cosmetic issues, minor divergences from convention | Address opportunistically |

---

## Modern Go audit — 2026-11-04

This scoped review covers `internal/dispatch`, `internal/installation` and their
related SQL/callers/tests against the adopted Go standards. It does not reconcile
all historical findings or establish production readiness.

| ID | Severity | Finding and remediation |
| --- | --- | --- |
| GA1 | P2 | Mobile identity/origin database calls and lifecycle transactions inherited caller contexts without an internal deadline; lifecycle rollback used unbounded Background. A held project row or stalled pool could retain connections indefinitely. Fixed with five-second operation budgets, context propagation through transaction callbacks/retries, and independent one-second cleanup. Earlier caller deadlines and ownership checks are preserved. |
| GA2 | P2 | Kafka source approval during Fetch could wait on PostgreSQL for the entire generation lifetime. Fixed with a five-second database-check budget; idle Kafka waiting still follows generation cancellation. |

Regression evidence and the remaining review boundary are recorded in
[the modern Go audit](docs/analysis/modern-go-audit.md).

## Replay authority review — 2026-11-04

| ID | Severity | Finding and remediation |
| --- | --- | --- |
| RA1 | P2 | The unwired replay verifier checked five seconds of JWT lifetime but could accept MFA freshness with less than that operation margin. Fixed by retaining the earlier JWT/MFA expiry and enforcing the margin in both verifier and DB grant checks. A focused red/green regression covers this boundary. |
| RA2 | P2 | Operational authority inherited the ACTIVE-app send precondition before inspecting existing marker/terminal evidence, preventing no-op reconciliation during app holds. Fixed by retaining current DB membership and send-role checks while applying app eligibility after marker/terminal/expiry classification. HELD queued/prepared/terminal red/green fixtures and registration no-execution assertions cover the boundary. |
| RA3 | P2 | Claim registered the full request before decoding a foreign-app selected position, leaving an unauthorized UUID reservation on denial. Fixed by checking every selected immutable evidence row inside registration's authority-locked transaction before header/audit insertion. A mixed-scope batch regression proves no header, attempt, control or audit reservation. |

The [registration verification](docs/analysis/m05-replay-registration-verification.md)
records the authority review boundary. [Execution verification](docs/analysis/m05-replay-execution-verification.md)
now covers the unwired publication/recovery libraries, proof reuse and commit
uncertainty. Pinned Kafka cancellation can retain producer-owned I/O; source
handoff therefore requires producer drain. CLI/runtime/deployment gates remain open.

## Replay command configuration review — 2026-11-05

| ID | Severity | Finding and remediation |
| --- | --- | --- |
| RC1 | P2 | Slash-only or encoded-slash database URI paths passed the explicit-name check but pgx normalized them to an absent database. Fixed by rejecting an empty effective path and comparing the parsed database name. Red/green URI regressions cover both forms. |
| RC2 | P2 | pgx merged ambient `PG*`/service policy and home certificate defaults into the supposedly explicit replay configuration. Fixed by rejecting nonempty `PG*` settings, overriding unconfigured certificate paths and disabling implicit passfile lookup. Environment regressions and focused race tests pass; the process environment is never changed by the loader. |

Additional executable/session review:

| ID | Severity | Finding and remediation |
| --- | --- | --- |
| RC3 | P2 | Cancellation while reading incomplete input was reported as usage exit 2. Fixed by preferring context cancellation over the resulting pipe-read error; canceled-reader and real-pipe regressions pass. |
| RC4 | P2 | Root ownership/POSIX modes alone missed Darwin extended ACL write/security/delete grants. Fixed by rejecting nonempty extended ACLs on every config/ancestor/DB TLS path; native Darwin and Linux ACL regressions pass, with unsupported inspection failing closed. |
| RC5 | P2 | Implicit system roots could inherit `SSL_CERT_FILE`/`SSL_CERT_DIR` outside trusted JSON. Fixed by rejecting nonempty overrides at load and again before session startup; environment regressions pass. |

The executable/session now also has local PostgreSQL verified-TLS and Kafka mTLS
command evidence. Production policy/issuer provisioning, incarnation/ACL approval
and supervised drain/handoff remain open. See the
[verification record](docs/analysis/m05-replay-command-verification.md).

## Scheduler runtime review — 2026-11-05

| ID | Severity | Finding and remediation |
| --- | --- | --- |
| SR1 | P2 | The unused due-schedule query included old runtime epochs, which could fill the first bounded page once polling was wired. A real PostgreSQL red regression selected an old-origin row ahead of the live campaign. Fixed by joining exact current lineage/unsealed runtime while retaining expired schedules and migration-held bookkeeping. |
| DR1 | P2 | New daemon entry points could block forever writing to a full output pipe during SIGTERM, preventing joined cleanup. Fixed by closing process-owned stdout/stderr on cancellation and joining the interruption callback; full-pipe diagnostics/help regressions pass with the race detector. |

Independent review found no concrete new fault in authenticated transport
propagation, source-before-outbox locking, per-candidate promotion or loop
cancellation/join ownership. Protected executable configuration/wiring is now
locally verified; production gates remain open. See
[runtime composition](docs/specs/m04-m05-runtime-composition.md).

## M06 execution review — 2026-11-05

| ID | Severity | Finding and remediation |
| --- | --- | --- |
| ER1 | P2 | Separate clock_timestamp evaluations for reserved-at and retry floor could persist a floor one microsecond shorter than the required interval because UPDATE assignment evaluation order is unspecified. A PostgreSQL regression observed 9.999999s for a 10s floor. Fixed by one materialized reservation-clock value for both persisted timestamps, retaining fresh DB-clock lease/deadline guards. |

Narrow independent review checked constraints, parent/child lock order,
DB-clock fencing, takeover and acknowledged-commit proof issuance. It found no
additional concrete blocker in this slice. Provider/result/recovery completion
is not implied by this review. See the
[verification record](docs/analysis/m06-execution-verification.md).

## M06 result review — 2026-11-05

| ID | Severity | Finding and remediation |
| --- | --- | --- |
| RR1 | P2 — fixed | Truncating a nanosecond provider retry hint to PostgreSQL microsecond precision could shorten its floor by less than one microsecond. A focused red/green test reproduces this; next-at rounding now uses the ceiling before deciding deadline eligibility. |

Focused independent review found no additional concrete result fencing, lock-order,
conditional invalidation, commit uncertainty or atomic outbox fault. Legacy migration
fixtures now unwind migration 000030 before dropping their dependent intent tables.

## M06 update publication review — 2026-11-05

| ID | Severity | Finding and remediation |
| --- | --- | --- |
| UP1 | P2 — fixed | A due current-lineage successor blocked by a retained foreign-lineage predecessor was indistinguishable from an idle queue. Such work now reports Held and retains both rows for coordinated reconciliation; it never silently bypasses/deletes old evidence. A mixed-epoch regression covers the policy. |

Review found no additional concrete same-lineage ordering, ack/delete atomicity,
JSON ambiguity or malformed-envelope fairness defect. Actual producer-owned late
I/O still requires shutdown drain and consumer event-ID/version deduplication.

## Receipt capability/payload review — 2026-11-05

| ID | Severity | Finding and remediation |
| --- | --- | --- |
| RP1 | P2 | Length-only Ed25519 public-key configuration admitted the identity point, under which a fixed signature verifies arbitrary messages. This requires a bad configured trust key, not attacker control over the ring. Fixed by canonical-y/small-order screening (both signs and negative-zero aliases) before retaining receipt, operational replay, mobile JWT and signed-origin trust keys; standard-library verification remains mandatory. Regressions reproduce the forgery and reject the key. |
| RP2 | P2 | Pointer-only signer formatting redaction could be bypassed by copying/dereferencing its exported Go value. Fixed with value-receiver formatting/slog/JSON redaction and pointer/copied-value regressions. |
| RP3 | P2 | Routing-only destination metadata could pair one binding's address with another binding's receipt scope on the same route. Fixed by comparing destination binding ID, installation public UUID and owner epoch with the signed scope; mismatched identity tests pass. Current-DB eligibility/decryption remains the executor's separate responsibility. |

Independent follow-up review found these three issues addressed. Key screening
uses the mathematical low-order encodings documented by
[libsodium 1.0.18](https://github.com/jedisct1/libsodium/blob/1.0.18/src/libsodium/crypto_core/ed25519/ref10/ed25519_ref10.c#L965-L1017)
and rejects noncanonical aliases; it is not full curve/provenance validation.
See [verification](docs/analysis/m06-receipt-payload-verification.md).

## M06 FCM transport review — 2026-11-05

| ID | Severity | Finding and remediation |
| --- | --- | --- |
| FT1 | Medium — fixed | Shared quota cooldown reused the per-delivery retry timestamp, which is capped at that delivery's deadline. A short-lived delivery could shorten a project-wide `Retry-After` hold. Shared cooldown now computes the provider/floor delay independently. `TestQuotaHoldSurvivesDeliveryDeadline` checks the individual terminal deadline and longer shared admission deferral. |

Focused independent review found no additional concrete route/auth isolation,
hidden retry, response-bound or cancellation/join fault. This does not prove
real Firebase/APNs delivery or authorize production execution.

## Critical Findings

Crashes, security bypasses, data corruption, or issues that affect every user of a given endpoint.

### Security & Authentication

| # | Location | Issue | Impact |
|---|----------|-------|--------|
| C1 | `internal/middleware/api_key.go` | API key compared with `!=` rather than `crypto/subtle.ConstantTimeCompare` | Timing side-channel — an attacker with network access can recover the shared key byte-by-byte |
| C2 | `internal/features/auth/service.go` — `RefreshToken` | **Original finding superseded by existing correct code:** role lookup uses staff.RoleID and ignores the stale token role | M02e real-DB regression with staff ID 700001/current ADMIN/stale VIEWER refresh claim returns ADMIN access. This slice verifies the existing fix; it does not introduce session rotation. |
| C3 | `internal/features/users/device_tokens_handler.go` — `Register` | `user_id` taken from request body; only the shared API key gates the endpoint | Any caller with the API key can register a device for any user |
| C4 | `internal/features/users/device_tokens_handler.go` — `Delete` | No ownership check — deletes any device token by ID | Any caller with the API key can remove any device registration |
| C5 | `internal/middleware/audit.go` — `extractClientIP` | Trusts `X-Forwarded-For` without verifying a trusted proxy terminates the request | IP spoofing in audit trail if the service is ever exposed directly |
| C6 | `internal/features/staff` — administrative authority | **Remediated in M02b, locally verified 2026-11-03:** all seven routes and public service methods require active current-DB SUPER_ADMIN via `staff.manage`; JWT role gate removed from server wiring | [Evidence](docs/analysis/m02b-verification.md): four-role/seven-route matrix, stale JWT/committed demotion, direct-service fail-closed checks. Statement snapshots, not transactional revocation; not deployed. |
| C7 | `internal/features/staff/service.go` — `UpdatePassword` | **Original claim superseded:** actor password confirmation and 8–72-byte reset bounds already existed; M02b preserves them and adds shared current-authority checks plus fail-closed actor lookup | Reset success/rejection and actor lookup failures pass unit tests; real-DB reset and wrong-password tests pass. Migration 000033 now advances the staff authentication generation on password reset. Password creation policy is separate. |
| C8 | `internal/middleware/jwt.go` — `JWTMiddleware` | **Original finding superseded:** middleware checks IsActive before the handler; inactive/deleted accounts are denied | M02a/b current-authority and committed lifecycle tests pass. M42 adds a current database session and generation check to each staff request. |
| C21 | `internal/authz`; notification/segment/member routes and services | **Fixed in code, locally verified 2026-11-02 (M02a):** current DB authority guards cover all 14 routes, nine mutation services and non-null create/PATCH scheduling input; creators are principal-derived | [Verification](docs/analysis/m02a-verification.md): four-role real-DB/API matrix, direct service denials, stale claims/committed demotion and no-write/no-Kafka tests pass. Statement-time authorization is not a lock through mutation; staff administration/session/atomic audit remain follow-up. Not deployed. |

### Runtime Crashes (Panics)

| # | Location | Issue | Impact |
|---|----------|-------|--------|
| C9 | `internal/token/jwt.go` — `ParseAccessToken` | `token.Claims.(*AccessClaims)` type assertion runs before the `err` check | Panic on malformed input where the JWT library returns a nil token |
| C10 | `internal/token/jwt.go` — `ParseRefreshToken` | Single-value type assertion on `parsedToken.Claims` | Panic on every parse failure path |
| C11 | `internal/features/notifications/mock_provider.go` — `Send` | Slices `token.PushToken[:8]` without a length guard | Panic for any push token shorter than 8 bytes |

### Data Correctness (High Blast Radius)

| # | Location | Issue | Impact |
|---|----------|-------|--------|
| C12 | `internal/features/notifications/service.go` — `Send` | Publishes the Kafka event but does not transition the notification to `QUEUED` — status remains `DRAFT` | Two consecutive `Send` calls publish two events and trigger two delivery rounds; concurrent processing can double-deliver to every device. Between publish and worker processing, the row remains editable and deletable |
| C13 | `internal/features/notifications/processor.go` — `ProcessSendRequested` | `producer` field is injected but never invoked | The worker never publishes `NotificationDeliveryUpdated` events, contradicting the README. Downstream analytics receives nothing |
| C14 | `internal/features/segments/members_service.go` — `AddMember` | Existence check uses `ListBySegment(..., 0, 1)` — only the first member is inspected | Duplicate membership rows or unique-violation responses (served as 500) for any segment with more than one member |
| C15 | `internal/features/segments/members_service.go` — `RemoveMember` | Assumes `DeleteBySegmentAndUser` returns an error for a missing row | `ErrMemberNotFound` is unreachable; removing a non-member returns 200 "member removed" for a removal that never occurred |
| C16 | `internal/features/users/service.go` — `List` (external_id fast path) | Every repository error is swallowed and replaced with `([]User{}, 0, nil)` | A database failure is indistinguishable from "no such external_id" — clients receive a successful 200 with empty results |
| C17 | `internal/kafka/producer.go` — `PublishDeliveryUpdated` | Message key built with `string(rune(event.NotificationID))` — a Unicode code point, not the decimal representation | Partitioning contract violated: ID 65 produces `"delivery_A"`; IDs in the surrogate range collapse to `U+FFFD`. Events for the same notification may land on different partitions |
| C18 | `internal/features/segments/service.go` — `GetByID`, `Create`, `Update` | `Actor.ID` reads `actor.ID` even when the staff lookup failed | Responses carry `{id: 0, name: ""}` for the creator reference, discarding the correct value held in `seg.CreatedBy` |
| C19 | `internal/features/users/service.go` — `GetUserNotifications` | Paginates over `notification_deliveries` rows (one per device) rather than distinct notifications | A user with N devices over-counts by a factor of N in both the page and the `Total` field |
| C20 | `internal/features/users/service.go` — `UpdateDeviceToken` | Metadata and status are written by two separate repository calls with no transaction | A failure on the second write leaves the token with updated metadata but an unchanged active flag; the client receives an error naming only the second step |

---

## High Findings

User-visible bugs, security weaknesses without immediate exploitability, and behaviours that silently produce incorrect results.

### Error Handling & Response Semantics

| # | Location | Issue |
|---|----------|-------|
| H1 | `internal/features/staff/handler.go` — `Update` | `ErrEmailAlreadyUsed` is not mapped — falls through to a 500 rather than the intended 409 |
| H2 | `internal/features/auth/handler.go` — `RefreshToken` | `ErrInvalidCredentials` is not mapped — a deactivated account's refresh attempt returns 500 instead of 401 |
| H3 | Feature-handler unknown 500 branches | **Original broad claim superseded:** reviewed current feature-handler 500 branches use literal internal server error; M02a/b authz error tests verify scoped failures | Source review is not complete redaction coverage for all mapped 4xx, DTOs, logs or future branches. See M02 assessment; production error/security review remains required. |
| H4 | `internal/features/notifications/processor.go` | Status-update errors on the `FAILED` and `COMPLETED` paths are discarded with `_ =` |
| H5 | `internal/features/profile/service.go` — `GetProfile`, `UpdateProfile` | Every repository failure is collapsed into `ErrProfileNotFound` — a database outage surfaces as 404 |
| H6 | `internal/features/segments/service.go` — `GetByID`, `Create`, `Update` | Same collapse pattern for `ErrSegmentNotFound` |
| H7 | `internal/features/users/service.go` — `GetByID` | Same pattern — a transient DB failure is reported as 404 |
| H8 | `internal/features/staff/service.go` — all read methods | Same pattern for `ErrStaffNotFound` across `GetByID`, `Update`, `UpdateStatus`, `UpdatePassword`, and `Delete` |

### List & Pagination

| # | Location | Issue |
|---|----------|-------|
| H10 | `internal/features/segments/service.go` — `List` | Total uses the unfiltered `Count(ctx)` even when a search is applied — pagination metadata is wrong for every filtered list |
| H11 | `internal/features/staff/service.go` — `List` | Same defect |
| H12 | `internal/features/users/service.go` — `List` | Same defect, compounded by filters that do not compose — `external_id` takes priority over `keyword`, which takes priority over `status`; only one filter is honoured per request |
| H13 | `internal/features/users/service.go` — `List` (external_id path) | Pagination is ignored entirely — `total: 1` on every page, regardless of the requested `page` and `limit` |
| H14 | `internal/features/notifications/service.go` — `List` | `&row.ScheduledAt.Time` is taken for NULL columns — the resulting non-nil pointer carries the zero time `0001-01-01T00:00:00Z`, which a client renders as a valid-looking timestamp |
| H15 | `internal/features/notifications/service.go` — `List` | `SentAt` is hardcoded to nil; the unfiltered path hardcodes `Type: "BROADCAST"` for every row |
| H16 | `internal/features/segments/service.go` — `List` | N+1 member-count queries — one per segment on the page, up to 100 additional round trips |
| H17 | `internal/features/segments/service.go` — `List` | Member-count errors are discarded with `_ =` — a failed count is reported as zero members |
| H18 | `internal/features/users/service.go` — `Search`, `ListDeviceTokens`, `GetUserSegments` | Hard-coded result caps (20, 100, 100) with no pagination and no truncation indicator |

### Provider Contract Divergence

| # | Location | Issue |
|---|----------|-------|
| H19 | `internal/features/notifications/onesignal_provider.go` — `Send` | Tokens shorter than or equal to 10 bytes are silently dropped — the returned slice may be shorter than the input, breaking the per-token contract that the processor assumes |
| H20 | `internal/features/notifications/onesignal_provider.go` — `Send` | HTTP status code is not inspected — a 401 response with an empty `Errors` array marks every token as `"SENT"` |
| H21 | `internal/features/notifications/onesignal_provider.go` — `Send` | Returns `nil, err` on batch failure, unlike `FCMProvider` which returns per-token results with a nil function-level error |
| H22 | `internal/features/notifications/onesignal_provider.go` — `Send` | Single `ProviderMessageID` for the entire batch — per-device receipt correlation is impossible |
| H23 | `internal/features/notifications/onesignal_provider.go` — `Send` | `Recipients` field is not consulted — a partial rejection is reported as full success |
| H24 | `internal/features/notifications/fcm_provider.go` — `Send` | Function-level error return is always nil — all failures are per-result, and a caller checking only the error return sees an all-successful send |
| H25 | `internal/features/notifications/fcm_provider.go` — `Send` | Sequential per-token sends — the SDK's `SendAll` batching method is unused |
| H26 | `internal/features/notifications/fcm_provider.go` — `Send` | Hardcoded `click_action: "FLUTTER_NOTIFICATION_CLICK"` — a client-side integration assumption embedded in the server |

### Idempotency & Race Conditions

| # | Location | Issue |
|---|----------|-------|
| H27 | `internal/features/notifications/processor.go` | Idempotency guard covers only `"COMPLETED"` and `"FAILED"` — a notification left in `"PROCESSING"` by a crashed predecessor is re-dispatched on retry |
| H28 | `internal/features/notifications/processor.go` | Device attribution iterates by `UserID` and breaks on first match — every delivery row for a multi-device user is recorded against the same device |
| H29 | `internal/features/notifications/processor.go` | `expandTargets` uses `10,000` for target rows and `1,000,000` for user and member lists — effectively unbounded pagination with silent truncation above those thresholds |
| H30 | `internal/features/mobile/service.go` — `Sync` | Platform validation runs **after** the user upsert — an invalid platform produces a 400 with a user row already created or modified |
| H31 | `internal/features/mobile/service.go` — `registerDeviceToken` | Device tokens are not transferred between users — a device handed to a new owner remains attributed to the original owner |
| H32 | `internal/features/mobile/service.go` — `registerDeviceToken` | The `Exists → Create` sequence is not transactional — two concurrent syncs with the same push token can both attempt an insert |
| H33 | `internal/features/profile/service.go` — `UpdateProfile` | TOCTOU between `ExistsByEmail` and `Update` — concurrent updates claiming the same email either produce a unique-violation 500 or duplicate email rows, depending on the schema |
| H34 | `internal/features/segments/service.go` — `Create`, `Update` | Advisory name-uniqueness check with the same TOCTOU properties |
| H35 | `internal/features/segments/service.go` — `Delete` | Member-count guard is not atomic with the delete — a concurrent `AddMember` can race past the check |
| H36 | `internal/features/staff/service.go` — `Create`, `Update` | Advisory email-uniqueness check |
| H37 | `internal/features/users/service.go` — `RegisterDeviceToken`, `UpdateDeviceToken` | Advisory push-token-uniqueness check |

### Partial Commits & Stale State

| # | Location | Issue |
|---|----------|-------|
| H38 | `internal/features/segments/service.go` — `Update` | A failure of the post-update re-fetch returns 500 even though the write has already committed |
| H39 | `internal/features/staff/service.go` — `Update`, `UpdateStatus` | Same defect; the audit entry is also dropped when the re-fetch fails |
| H40 | `internal/features/notifications/service.go` — `Update` | Post-update re-fetch failure is mapped to `ErrNotificationNotFound` (404) despite the write having landed |
| H41 | `internal/features/notifications/service.go` — `Create` | Parent notification and target rows are inserted without a transaction; the compensating delete is best-effort and its error is discarded, leaving orphaned notifications on failure |
| H42 | `internal/features/segments/service.go` — `Create` | Same pattern — no transaction around the parent and target inserts |

### Configuration & Lifecycle

| # | Location | Issue |
|---|----------|-------|
| H43 | `cmd/server/main.go` | **Fixed, locally tested 2026-11-08.** SIGINT/SIGTERM stop acceptance, allow active HTTP work to drain and then cancel overdue requests. The pool closes after handler exit. A hard-stop deadline reports failure when a handler remains unresponsive. See [process lifecycle](docs/specs/process-lifecycle.md). |
| H44 | `internal/features/notifications/module.go` | The Kafka producer created in the module constructor is never closed |
| H45 | `internal/kafka/producer.go` — `NewProducer` | `AllowAutoTopicCreation: true` means a typo in the configured topic name silently creates a new topic that no consumer reads |
| H46 | `internal/kafka/producer.go` — `NewProducer` | `RequiredAcks: RequireOne` — a leader failure immediately after acknowledgement can lose the message |
| H47 | `internal/kafka/producer.go` — `NewProducer` | **Open, verified 2026-11-02:** `kafka.LeastBytes` does not hash message keys, contradicting the claimed per-notification partition affinity. Pinned kafka-go v0.4.51 source and a local six-message probe over three partitions show the same key routed to 0/1/2. Preserve key bytes, use an explicit tested hash partitioner and review the mapping transition; ADR-07 is proposed only. |
| H48 | `internal/kafka/producer.go` — `NewProducer`; `notifications/processor.go` — delivery publish loop | **Open, source-verified 2026-11-02:** synchronous per-delivery publishes inherit kafka-go's default BatchSize=100 and BatchTimeout=1s. A serial caller cannot fill its own batch while waiting for ack; sparse publishes can accumulate batch waits and delay campaign finalization/update propagation. Broker latency has not been benchmarked. Explicit low-latency batching and bounded timeout/retry behavior are required for ADR-03/05 dispatchers. |

---

## Medium Findings

Design inconsistencies, duplication, contract drift, and maintainability hazards.

### Duplication

| # | Location | Issue |
|---|----------|-------|
| M1 | `audit.GetStaffID`, `notifications.getStaffIDFromContext`, `profile.GetStaffIDFromContext`, `segments.getStaffIDFromContext`, `users.getStaffIDFromContext` | Five identical copies of the staff-identity extraction helper; the `users` copy is unreferenced |
| M2 | `notifications.parseInt64FromPath`, `segments.parseInt64FromPath`, `staff.parseInt64FromPath`, `users.parseInt64FromPath` | Four identical copies |
| M3 | `getPaginationParams` in four packages | Four identical copies, all with the same silent-clamping policy |
| M4 | `notifications.Pagination`, `segments.Pagination`, `users.Pagination` | Three identical struct declarations |
| M5 | `notifications.StaffBrief`, `segments.StaffBrief` | Two copies of the same two-field DTO |
| M6 | `staff.StaffResponse`, `profile.ProfileResponse` | Field-identical DTOs in separate packages |
| M7 | `users.User` vs `mobile.MobileUser` | Field-identical domain views of the same table |
| M8 | `users.DeviceToken` vs `mobile.MobileDevice` | Same divergence for the device-tokens table |

### Dead Code

| # | Location | Issue |
|---|----------|-------|
| M9 | `internal/features/notifications/handler.go` | `contextKeyStaffID` type declared but never used |
| M10 | `internal/features/users/handler.go` | `getStaffIDFromContext` declared but never referenced |
| M11 | `internal/features/users/service.go` | `readRepo` field stored but never invoked |
| M12 | `internal/features/segments/members_handler.go` | Duplicate 404 branch for `ErrMemberNotFound`, which the service never returns |
| M13 | `internal/features/notifications/model.go` | `StatusSending` constant declared but never referenced |
| M14 | `internal/features/staff/interfaces.go` | `StaffUserRepository.Count` and several others declared but not used by the service |

### Contract Inconsistencies

| # | Location | Issue |
|---|----------|-------|
| M15 | `internal/features/notifications/model.go` | Constants cover `DRAFT`, `SCHEDULED`, `SENDING`, `SENT`, `FAILED`; the code writes raw strings `"PROCESSING"`, `"COMPLETED"`, `"QUEUED"` |
| M16 | `internal/features/notifications/dto.go` — `NotificationStatistics` | `Delivered` and `Opened` counters overlap — a row can contribute to both |
| M17 | `internal/features/auth/handler.go` — `RefreshToken` | Returns `res` directly — the handler is coupled to the concrete service return type |
| M18 | `internal/features/auth/model.go` — `User.Password` | Field named `Password` with tag `json:"password"` carries the bcrypt hash |
| M19 | `internal/features/notifications/handler.go` — `Send` | The 202 status presumes the Kafka publish succeeded; the ordering between status transition and publish is unspecified |
| M20 | `internal/features/segments/handler.go` — `List`, `GetByID` | `&s.Description` always produces a non-nil pointer — the `omitempty` tag is inert |
| M21 | `internal/features/users/dto.go` — `UserResponse` | `omitempty` on the timestamp fields is inert because `time.Format` never returns an empty string |
| M22 | `internal/features/notifications/handler.go`, `internal/features/staff/handler.go` | Update responses return a small map in one feature and the full updated record in the other |
| M23 | `internal/features/segments/dto.go` | `SegmentListItem` and `SegmentDetail` are field-identical yet declared separately |
| M24 | `internal/features/staff/handler.go` — `Update` | Request DTO uses plain strings, so a field cannot be set to an empty value |
| M25 | `internal/features/staff/dto.go` — `UpdateStatusRequest` | `IsActive bool` cannot distinguish "omitted" from `false` |
| M26 | `internal/features/profile/dto.go` — `UpdateProfileRequest` | Same plain-string limitation — no true PATCH semantics |
| M27 | `internal/features/notifications/dto.go` — `NotificationListItem.Type` | The inline comment "default SEGMENT because no join" reflects a field that does not reliably carry the real target type |

### Repository Layer

| # | Location | Issue |
|---|----------|-------|
| M28 | `internal/database/repository/*` | Most methods are pure pass-through wrappers around `sqlc.Queries` with no added value |
| M29 | `internal/database/repository/*` | `Search` methods wrap the keyword in `pgtype.Text{Valid: true}` unconditionally — an empty keyword is treated as a valid search term, unlike the `List` methods |
| M30 | `internal/database/repository/*` | `Create` vs `CreateFull` return different projections, forcing callers to know which variant produces which type |

### Interface vs Concrete Divergence

| # | Location | Issue |
|---|----------|-------|
| M31 | `internal/features/segments/interfaces.go` | The only feature module that declares repository interfaces |
| M32 | `internal/features/segments/members_service.go` | `MembersService` uses concrete repositories while `SegmentService` uses interfaces within the same package |
| M33 | `internal/features/staff/interfaces.go` — `StaffServicer` | The only handler-side interface in the codebase |
| M34 | `internal/features/users/service.go` | Uses concrete repositories throughout, unlike `segments` |

### Logging & Observability

| # | Location | Issue |
|---|----------|-------|
| M35 | `internal/observability/http.go` | **Fixed.** Server access records use structured JSON with bounded logging. |
| M36 | `internal/observability/http.go` | **Fixed.** Access records include a server-generated request ID, template, committed status, bytes, elapsed time and outcome. Raw client addresses stay out of access logs. |
| M37 | `internal/observability/http.go` | **Fixed.** A pre-commit panic returns a generic 500. A post-commit panic aborts the partial response. Both paths have regression tests. |
| M38 | Operations | Private metrics, dashboard and local rules are implemented. Distributed traces and target alert routing remain open. |
| M39 | `internal/middleware/jwt.go` | Token parse errors are not logged — diagnosing authentication failures requires reproducing them |

### Security Gaps (Non-Critical)

| # | Location | Issue |
|---|----------|-------|
| M40 | `internal/token/jwt.go` — both staff parsers | **Fixed locally in M02e:** pin HS256, require exp and positive user_id in addition to purpose/signature checks |
| M41 | `internal/token/jwt.go` | No `iss`, `aud`, or `sub` enforcement |
| M42 | `internal/token/jwt.go`, staff auth, migration 000033 | Fixed locally. Single-use refresh rotation detects replay, logout revokes a family, and credential/role/activity changes invalidate its staff generation. [Session contract and recovery](docs/specs/staff-sessions.md). |
| M43 | `internal/features/auth` | **Partially fixed in M02d:** login/refresh use the 64KiB full-body limit with safe 413/400; real-module rejection tests pass locally. Content-Type enforcement remains open for compatibility. |
| M44 | All 16 JSON input handlers, including profile | **Fixed in local code, M02d:** 64KiB small JSON / 1MiB campaign limits, complete bounded reads and single-value decoding. [Evidence](docs/analysis/m02d-verification.md): boundary/direct-handler/real-DB route tests; no rejected mutation or Kafka event. Other abuse controls remain open. |
| M45 | HTTP layer | No rate limiting on `/api/v1/auth/login` |
| M46 | HTTP layer | No CORS configuration for the admin portal |
| M47 | `internal/features/mobile/service.go` — `verifyJWT` | Correctly enforces HMAC signing — but `internal/token` does not apply the same hardening |

### Kafka Client Configuration

| # | Location | Issue |
|---|----------|-------|
| M48 | `internal/kafka/producer.go` — `NewProducer`; `cmd/worker/main.go` — reader config | **Open, source-verified 2026-11-02:** the producer Godoc promises comma-separated bootstrap seeds, but `kafka.TCP(broker)` passes one string and the reader likewise uses `[]string{cfg.KafkaBroker}`. Pinned kafka-go TCP is variadic and does not split CSV. Parse/validate an endpoint slice and use both writer/reader seed APIs correctly; real multi-broker failover is untested. |

---

## Low Findings

Cosmetic issues, minor divergences, and code smells.

| # | Location | Issue |
|---|----------|-------|
| L1 | `internal/audit/constants.go` | Constants use `SCREAMING_SNAKE_CASE` — non-idiomatic Go naming |
| L2 | `internal/token/jwt.go` | `StaffID` serialises as `json:"user_id"` — field name does not match tag |
| L3 | `internal/bootstrap/bootstrap.go` | `"SUPER_ADMIN"` is hardcoded instead of using `cfg.SuperAdminRole` |
| L4 | `internal/bootstrap/bootstrap.go` | No transaction wrapping the role lookup and staff insert |
| L5 | `internal/database/db.go` | No connection pool sizing configuration |
| L6 | `internal/config/scheduler.config.go` | `SchedulerInterval` is the only field with a non-empty default — asymmetric with the rest of the config surface |
| L7 | `internal/config/base.config.go` — `GetDatabaseURL` | **Fixed.** DB credentials, database names and SSL mode are encoded as separate URL components. Tests round-trip reserved characters and IPv6 hosts. |
| L8 | `README.md` | Claimed versions (`Go 1.26.4`, `Kafka 4.3.1`) do not match released versions |
| L9 | `README.md` | Claims the worker emits `NotificationDeliveryUpdated` events — contradicted by C13 |
| L10 | `internal/features/notifications/dto.go` — `Pagination` | The `Limit` field silently reflects the clamped value, not the client's request |
| L11 | `internal/features/auth/model.go` — `User.Password` | Field name is misleading about the value it holds |
| L12 | `internal/features/segments/handler.go` — `List`, `GetByID` | `&s.Description` takes the address of a plain string |
| L13 | `internal/features/segments/members_handler.go` | Hand-rolled format string `"2006-01-02T15:04:05Z"` — the trailing `Z` is a literal, not a timezone indicator |
| L14 | `internal/features/users/dto.go` — `OpenedAt` | `*string` instead of `*time.Time` — the only such case in the codebase |
| L15 | `internal/features/users/model.go` — `UserNotification.OpenedAt` | Same divergence in the domain model |
| L16 | `internal/features/staff/handler.go` — `List` | Pre-computes `TotalPages` — the only list response in the codebase that does so |
| L17 | `pkg/response/response.go` | JSON encoding errors are silently discarded — a truncated response is served with no indication |
| L18 | `internal/kafka/events.go` | Event structs carry no event ID or schema version |
| L19 | `internal/features/notifications/dto.go` — `NotificationListItem` | `Type` defaults to `"SEGMENT"` on the unfiltered list path — misleading when the actual type is different |
| L20 | `internal/features/segments/service.go` — `Update` | Writes and audits even when no field has changed, producing a no-op audit entry |

---

## Cross-Cutting Patterns

Recurring issues that appear across multiple features. Fixing each pattern once eliminates a class of findings rather than a single instance.

### 1. Repository Error Collapse

Every feature maps all repository failures from a by-ID lookup into its `ErrXxxNotFound` sentinel. A database connectivity failure, a query error, or a driver-level problem is therefore reported to clients as 404.

**Affected:** `auth`, `mobile`, `notifications`, `profile`, `segments`, `staff`, `users`.

**Recommended fix:** Distinguish `pgx.ErrNoRows` from other errors. Return the not-found sentinel only for the former; propagate everything else as a wrapped error so the handler can produce a 500.

---

### 2. `err.Error()` Disclosure on the Default 500 Branch

Every handler except `auth` serves `err.Error()` as the message body of its 500 responses. Repository errors, wrapped driver messages, and future internal sentinels are therefore disclosed verbatim.

**Affected:** `mobile`, `notifications`, `profile`, `segments`, `staff`, `users`.

**Recommended fix:** Adopt the `auth` handler's convention of replacing the 500 message with a generic string. Log the underlying error server-side.

---

### 3. TOCTOU on Uniqueness Checks

Every uniqueness check (`ExistsByEmail`, `ExistsByName`, `ExistsByPushToken`) runs as a separate query before the write. Two concurrent requests can both pass the check and both attempt the write. The outcome depends entirely on whether the schema enforces a UNIQUE constraint, and on what the handler does with the resulting error.

**Affected:** `profile`, `segments`, `staff`, `users`, `mobile`.

**Recommended fix:** Rely on the schema constraint as the authoritative guard. Catch the unique-violation error at the repository layer and translate it into the corresponding sentinel before the handler sees it.

---

### 4. Post-Write Re-Fetch Failures Produce 500

Every `Update` method re-reads the row after the write and uses the result for the response and the audit entry. A failure of that second read returns an error that the handler serves as a 500, even though the write has already committed.

**Affected:** `notifications`, `profile`, `segments`, `staff`, `users`.

**Recommended fix:** Either accept the write's success as the operation's outcome and construct the response from the update params, or make the re-fetch failure non-fatal — fall back to returning the update params as the response state.

---

### 5. Five Copies of `getStaffIDFromContext`

The staff-identity extraction helper is duplicated across five packages. Any change to the context representation must be applied in all five places, or in the four if the dead copy in `users` is removed.

**Affected:** `audit`, `notifications`, `profile`, `segments`, `users`.

**Recommended fix:** Extract into a small dependency-free helper package (for example, `pkg/identity` or `internal/ctxutil`). All features import it. Removes the duplication and the dead code in one step.

---

### 6. Silent Pagination Clamping

Every `getPaginationParams` clamps `limit` to 100 and defaults to 10, without informing the client that its request was rejected or adjusted. The response echoes the applied values, but a client cannot distinguish "you asked for 10" from "you asked for 5000 and got 10".

**Affected:** `notifications`, `segments`, `staff`, `users`.

**Recommended fix:** Either reject out-of-range values with a 400, or include both the requested and applied limits in the response metadata.

---

### 7. Create-Only vs Upsert Divergence for Device Tokens

The mobile feature upserts an existing push token; the users feature returns a conflict. A client that uses the two endpoints interchangeably sees different results for the "device already registered" case.

**Affected:** `mobile`, `users`.

**Recommended fix:** Standardise on one behaviour. The upsert is generally correct for client-initiated registration, because the mobile client cannot know whether the server has already seen the device.

---

### 8. Nullable Timestamps as `*string` vs `*time.Time`

The majority of the codebase uses `*time.Time` for nullable timestamps. The users feature carries them as `*string` in both the domain model and the DTO, with formatting performed at the service layer.

**Affected:** `users`.

**Recommended fix:** Convert `UserNotification.OpenedAt` and `DeviceTokenResponse.LastSeenAt` to `*time.Time` and move the formatting to the handler. Brings the users feature in line with the rest of the codebase.

---

## Remediation Roadmap

### Sprint 1 — Critical

Focus: crashes, security bypasses, data corruption.

- [ ] C9, C10, C11 — eliminate the three panic paths in the token and mock-provider code
- [x] C2 — existing staff.RoleID refresh lookup reverified with a real-DB regression in M02e
- [ ] C3, C4 — add ownership checks to the device-token register and delete endpoints
- [ ] C12 — add the missing `QUEUED` transition and its guard in `NotificationService.Send`
- [ ] C13 — wire the delivery-update producer or remove the field and the README claim
- [ ] C14, C15 — replace the defective membership checks with proper existence queries
- [ ] C1 — switch the API key comparison to `crypto/subtle.ConstantTimeCompare`
- [ ] C17 — fix the `string(rune(...))` message-key construction in the Kafka producer

### Sprint 2 — High

Focus: user-visible bugs and contract divergences.

- [ ] H1–H8 — close the error-mapping gaps and adopt a consistent 500 policy
- [ ] H10–H18 — correct the totals, honour pagination, and remove silent caps
- [ ] H19–H26 — reconcile the two provider contracts and harden the OneSignal path
- [ ] H27–H37 — audit and correct the idempotency and TOCTOU behaviours
- [ ] H38–H42 — fix the partial-commit paths, either with transactions or with corrected response semantics
- [ ] C19 — make notification history report per-notification, not per-delivery
- [ ] C20 — wrap the device-token update in a transaction

### Sprint 3 — Medium

Focus: design consistency and maintainability.

- [ ] M1–M8 — consolidate the duplicated helpers and DTOs
- [ ] M9–M14 — remove dead code
- [ ] M15–M27 — reconcile the divergent contracts and naming conventions
- [x] M43 (body cap), M44 — bounded full-body reads for all 16 JSON inputs, locally verified M02d
- [ ] M43 — Content-Type enforcement and remaining request/abuse controls
- [x] H43 — graceful HTTP shutdown, forced cancellation and bounded handler join
- [x] M35–M37 — migrate to structured logging with a request ID

### Sprint 4 — Polish

Focus: operational readiness.

- [ ] M45 — add rate limiting to the login endpoint
- [ ] M46 — configure CORS for the admin portal
- [ ] M38 — add Prometheus metrics and OpenTelemetry tracing
- [ ] L1 — rename the audit constants to `MixedCaps`
- [ ] L8, L9 — correct the version claims in the README

---

## Architectural Recommendations

Beyond the itemised findings, five architectural changes would eliminate whole categories of issues in one pass.

### 1. Automated Testing

Staff and segments have mockery-based service tests; notifications, middleware
and token also have unit tests. The baseline was repaired on 2026-11-02 by
regenerating segment mocks and aligning fixtures/assertions with current
authorization, password and counting contracts. Green unit tests do not close
runtime findings or prove concurrency/TOCTOU safety; PostgreSQL/Kafka/provider
failure-window tests remain planned.

### 2. Shared Request Parsing

Extract `getStaffIDFromContext`, `parseInt64FromPath`, and `getPaginationParams` into a single package — `pkg/reqparse` or `internal/ctxutil`. Removes five, four, and four copies respectively, and gives future changes a single point of application.

### 3. Centralised Error-to-HTTP Mapping

Move the per-handler error switches into a shared mapper that takes a domain error and returns an HTTP status plus a safe message. Removes the divergence documented in H1–H3 and makes the disclosure policy consistent by construction.

### 4. Transaction Boundaries for Multi-Write Operations

Segment creation, notification creation, device-token update, and mobile sync all perform multiple writes that must commit together. Wrapping them in explicit `pgx.Tx` transactions would resolve C20, H41, H42, and several TOCTOU findings.

### 5. Kafka Event Schema Versioning

Adding an `event_id` and a schema version to the event structs would enable consumer-side deduplication and unblock the at-least-once → effectively-once transition that the delivery pipeline currently lacks.

---

## Summary Statistics

| Severity | Count |
|----------|-------|
| Critical | 21 |
| High | 47 |
| Medium | 48 |
| Low | 20 |
| **Total** | **136** |

Counts are recalculated from distinct finding IDs currently present in the
tables, including C21/H47/H48/M48; they are not counts of currently open findings.
The old summary undercounted High by one. Original closed/fixed rows still
need reconciliation.

**Cross-cutting patterns:** 8

**Packages affected:** all 7 feature modules, all 3 `cmd/` binaries, `internal/audit`, `internal/config`, `internal/database`, `internal/kafka`, `internal/middleware`, `internal/token`, `pkg/response`

---

## Changelog

| Version | Date | Description |
|---------|------|-------------|
| 1.0 | 2026-09-14 | Initial findings inventory compiled from the full-codebase Godoc pass |
| 1.1 | 2026-11-02 | Add open Kafka findings H47/H48/M48 from source/dependency review and key-partition probe; see `docs/analysis/kafka-readiness.md`. No runtime fix applied. |
| 1.2 | 2026-11-02 | Add open role-permission finding C21 from route/service/seed review and recalculate summary counts from distinct IDs; see `docs/analysis/api-data-security-readiness.md`. No runtime fix applied. |
| 1.7 | 2026-11-03 | M02 baseline assessment and scoped staff JWT hardening; reconcile C2/C8/H3 against current code/evidence. Full issuer/session/mobile/abuse policy remains open. |
| 1.6 | 2026-11-03 | Remediate M44 and the body-size portion of M43 with shared bounded JSON input; Content-Type and other abuse controls remain open. |
| 1.5 | 2026-11-03 | Fix new staff audit credential snapshots (including Create/After); prepare and locally verify historical batch cleanup. Production history/backups remain an operator gate. |
| 1.4 | 2026-11-03 | Reconcile C6/C7 and implement current-DB staff administration guards; retain session/transaction/audit redaction limitations. |
| 1.3 | 2026-11-02 | Remediate C21 in M02a with current-authority route/service guards and locally verified negative API/DB/Kafka cases; remaining security/transactional limitations recorded in `docs/analysis/m02a-verification.md`. |

---

## Questions?

For the design rationale behind any individual finding, refer to the corresponding Godoc comment in the source file — the pass added detailed explanatory comments at every location listed here.  
For the overall codebase architecture and development workflow, see the main `README.md`.  
For the database schema and entity relationships, see `docs/database.md`.
---
---

M02b update (2026-11-03): C6/C7 reconciled with current code and [local evidence](docs/analysis/m02b-verification.md). Existing staff/audit snapshots need a separate redaction review: Update/UpdateStatus/Delete pass full repository rows as Before payloads, including PasswordHash; M02b does not change these persisted payloads.

M02c update (2026-11-03): the staff audit exposure above is fixed for new writes
using explicit StaffSnapshot projections and a strict AuditService staff payload
guard. Create/After was also credential-bearing and is covered. Persisted fixture
audits and historical cleanup SQL pass local PostgreSQL tests; see
[evidence](docs/analysis/m02c-verification.md). Production history and historical
copies are not claimed cleaned; [operator procedure](docs/runbooks/staff-audit-redaction.md).
Audit durability/atomicity remains open; other entity payloads are unchanged.


Scoped segment routes address C14/C15 with the membership unique constraint and
DELETE RETURNING. They serialize name checks and segment mutations per project
(H34), and lock the segment before checking references (H35). These safeguards
apply to the project routes. Legacy route retirement is tracked separately.

The project campaign draft create route addresses H41 with a single transaction
for the campaign, targets and audit. Legacy route retirement is separate.
