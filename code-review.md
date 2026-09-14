# Notification Panel Backend — Code Review Findings

**Version:** 1.0  
**Last Updated:** 2026-09-14  
**Scope:** Full codebase audit conducted during the Godoc documentation pass  
**Coverage:** `cmd/`, `internal/`, `pkg/` — every non-test, non-generated `.go` file

---

## Overview

This document is the complete inventory of bugs, security gaps, correctness issues, and design weaknesses identified across the Notification Panel codebase during an exhaustive documentation review. Every item is drawn from the inline Godoc comments added in the same pass, so each finding has a corresponding in-code reference that explains the surrounding context, the conditions that trigger the issue, and the intended fix.

The findings are grouped by severity rather than by file, so that remediation can be prioritised without having to open every source file. A cross-cutting section follows the itemised lists and identifies patterns that recur across multiple features.

---

## Severity Legend

| Level | Meaning | Action |
|-------|---------|--------|
| **CRITICAL** | Crashes, data loss, security bypass, or corruption visible to end users | Fix immediately, before any new feature work |
| **HIGH** | User-visible bugs or security weaknesses that do not corrupt data | Fix within the current cycle |
| **MEDIUM** | Design inconsistencies, maintainability hazards, contract drift | Schedule for a dedicated refactor pass |
| **LOW** | Style, cosmetic issues, minor divergences from convention | Address opportunistically |

---

## Critical Findings

Crashes, security bypasses, data corruption, or issues that affect every user of a given endpoint.

### Security & Authentication

| # | Location | Issue | Impact |
|---|----------|-------|--------|
| C1 | `internal/middleware/api_key.go` | API key compared with `!=` rather than `crypto/subtle.ConstantTimeCompare` | Timing side-channel — an attacker with network access can recover the shared key byte-by-byte |
| C2 | `internal/features/auth/service.go` — `RefreshToken` | Passes `staff.ID` instead of `staff.RoleID` to `roleRepo.FindByID` | Minted access token carries a `role_name` claim that does not reflect the staff user's role, breaking downstream authorisation |
| C3 | `internal/features/users/device_tokens_handler.go` — `Register` | `user_id` taken from request body; only the shared API key gates the endpoint | Any caller with the API key can register a device for any user |
| C4 | `internal/features/users/device_tokens_handler.go` — `Delete` | No ownership check — deletes any device token by ID | Any caller with the API key can remove any device registration |
| C5 | `internal/middleware/audit.go` — `extractClientIP` | Trusts `X-Forwarded-For` without verifying a trusted proxy terminates the request | IP spoofing in audit trail if the service is ever exposed directly |
| C6 | `internal/features/staff/service.go` — `Create` | No caller authorisation check in the service layer | Any caller with API key + JWT can create a `SUPER_ADMIN` account; the HTTP-layer role gate is the only defence |
| C7 | `internal/features/staff/service.go` — `UpdatePassword` | No current-password check, no actor-authority check, no password policy | Admin can reset any account's password; no complexity or length enforcement anywhere in the codebase |
| C8 | `internal/middleware/jwt.go` — `JWTMiddleware` | Does not check staff active status | A deactivated staff user's existing tokens continue to work until expiry; no effective revocation |

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
| H3 | `internal/features/mobile/handler.go`, `internal/features/notifications/handler.go`, `internal/features/profile/handler.go`, `internal/features/segments/handler.go`, `internal/features/staff/handler.go`, `internal/features/users/handler.go` | Default 500 branch serves `err.Error()` verbatim — repository errors, wrapped driver messages, and future internal sentinels are all disclosed to clients. Only `auth` uses a generic string |
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
| H43 | `cmd/server/main.go` | No graceful shutdown — SIGTERM terminates in-flight requests immediately |
| H44 | `internal/features/notifications/module.go` | The Kafka producer created in the module constructor is never closed |
| H45 | `internal/kafka/producer.go` — `NewProducer` | `AllowAutoTopicCreation: true` means a typo in the configured topic name silently creates a new topic that no consumer reads |
| H46 | `internal/kafka/producer.go` — `NewProducer` | `RequiredAcks: RequireOne` — a leader failure immediately after acknowledgement can lose the message |

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
| M35 | `internal/middleware/logger.go` | Uses `fmt.Println` rather than the structured logger already in `go.mod` |
| M36 | `internal/middleware/logger.go` | Logs method and path only — no status code, duration, client IP, or correlation ID |
| M37 | `internal/middleware/logger.go` | No panic recovery — a handler panic propagates to `net/http`'s default path |
| M38 | Entire codebase | No metrics, no distributed tracing, no alerting integration |
| M39 | `internal/middleware/jwt.go` | Token parse errors are not logged — diagnosing authentication failures requires reproducing them |

### Security Gaps (Non-Critical)

| # | Location | Issue |
|---|----------|-------|
| M40 | `internal/token/jwt.go` — `ParseAccessToken` | Signing algorithm is not restricted to HS256 — HS384 or HS512 tokens signed with the same secret validate |
| M41 | `internal/token/jwt.go` | No `iss`, `aud`, or `sub` enforcement |
| M42 | `internal/token/jwt.go` | No revocation mechanism — logout is client-side only |
| M43 | `internal/features/auth/dto.go` | No request body size limit and no `Content-Type` check |
| M44 | `internal/features/mobile/handler.go`, `internal/features/segments/handler.go`, `internal/features/staff/handler.go`, `internal/features/users/handler.go`, `internal/features/notifications/handler.go` | Every handler decodes without a body size limit |
| M45 | HTTP layer | No rate limiting on `/api/v1/auth/login` |
| M46 | HTTP layer | No CORS configuration for the admin portal |
| M47 | `internal/features/mobile/service.go` — `verifyJWT` | Correctly enforces HMAC signing — but `internal/token` does not apply the same hardening |

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
| L7 | `internal/config/base.config.go` — `GetDatabaseURL` | Password is not URL-escaped — `@`, `:`, `#`, and `/` in the password produce a malformed DSN |
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
- [ ] C2 — fix the `staff.RoleID` reference in the auth refresh path
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
- [ ] M43, M44 — add request body size limits and `Content-Type` checks
- [ ] H43 — add graceful shutdown to the API server
- [ ] M35–M37 — migrate to structured logging with a request ID

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

Only `internal/features/staff/` has a `service_test.go`. Its mockery-based, table-driven structure is a sound template for every other feature. Testing the service layer with mocks would surface the error-collapse and TOCTOU issues documented here without a live database.

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
| Critical | 20 |
| High | 44 |
| Medium | 47 |
| Low | 20 |
| **Total** | **131** |

**Cross-cutting patterns:** 8

**Packages affected:** all 7 feature modules, all 3 `cmd/` binaries, `internal/audit`, `internal/config`, `internal/database`, `internal/kafka`, `internal/middleware`, `internal/token`, `pkg/response`

---

## Changelog

| Version | Date | Description |
|---------|------|-------------|
| 1.0 | 2026-09-14 | Initial findings inventory compiled from the full-codebase Godoc pass |

---

## Questions?

For the design rationale behind any individual finding, refer to the corresponding Godoc comment in the source file — the pass added detailed explanatory comments at every location listed here.  
For the overall codebase architecture and development workflow, see the main `README.md`.  
For the database schema and entity relationships, see `docs/database.md`.
---
---
