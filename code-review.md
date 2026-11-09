# Notification Panel Backend — Code Review Findings

**Version:** 1.7

**Last Updated:** 2026-11-09

**Scope:** Full codebase audit conducted during the Godoc documentation pass  
**Coverage:** `cmd/`, `internal/`, `pkg/` — every non-test, non-generated `.go` file

---

## Overview

This is a live inventory of bugs, security gaps, correctness issues, and design weaknesses. The original audit was drawn from the Godoc pass; later targeted reviews verify implementation and pinned dependency behavior directly. Older rows/checklists are not a reconciled list of still-open defects. Findings H47/H48/M48 were reconciled on 2026-11-09 against the pinned kafka-go source and now describe only the unmounted legacy producer; the mounted scoped transport already satisfies their remediation. The protected daemon uses the scoped Kafka transport described below. C21 was remediated and locally verified in M02a on 2026-11-02; see its row/evidence. Proposed ADRs alone do not fix runtime behavior.

The November 8 mount review traced `cmd/server` to the auth, applications,
staff and profile modules and to the configured installation/receipt routes.
The older user device-token handlers (C3/C4) and legacy notification processor
are outside that route inventory. The worker, scheduler and replay commands use
the scoped Kafka client with hashed keys, explicit broker addresses, a bounded
write and a single-record batch. The six-node drill exercised replica loss and
reader/writer failover. Reassess the historical findings before remounting any
legacy module. [Mounted contracts](docs/contracts/README.md) · [Kafka drill](docs/runbooks/kafka-replication-drill.md)

The findings are grouped by severity rather than by file, so that remediation can be prioritised without having to open every source file. A cross-cutting section follows the itemised lists and identifies patterns that recur across multiple features.

Dependency baseline update (2026-11-02): step 18's govulncheck found 24 reachable
advisories initially. Go/module security updates and a final scan reduced the
reachable result to zero; uncalled dependency advisories remain informational.
See [CI evidence](docs/analysis/ci-readiness.md) for versions and limitations.
The dependency scan does not close runtime findings; C21 is separately remediated by M02a code/tests. H47/H48/M48 are reconciled to the unmounted legacy producer; other older rows remain unreconciled.

## Mounted staff credential review — 2026-11-09

| ID | Severity | Finding and remediation |
| --- | --- | --- |
| AUTH1 | P2 | The pinned bcrypt comparison accepted a valid 72-byte password with extra bytes appended. The mounted admin reset and profile change now reject overlong current proofs before comparing. Both keep their existing authentication status and leave the hash and authentication epoch unchanged on rejection. |
| AUTH2 | P2 | Login returned before bcrypt for unknown or inactive staff and invalid stored hashes. Those paths now perform a default-cost fallback comparison and retain the same generic 401. Historical valid hashes keep their stored cost. Gateway and fleet throttling budgets remain target checks. |
| AUTH3 | P2 | Send, send replay and schedule trusted the HTTP session check after waiting for project and campaign locks. Acceptance now locks session lineage, actor and session in its transaction and checks expiry again before commit. PostgreSQL regressions cover revocation during a project lock wait and expiry during a campaign lock wait, with no persisted acceptance, key, audit or outbox changes. |
| AUTH4 | P2 | Registry and scoped draft, segment, membership and template writes trusted the earlier HTTP session check. Both transaction paths now lock and recheck the current staff session. Grant writes also lock the actor and target in staff ID order, and an enable checks target activity after obtaining those locks. Local PostgreSQL tests cover revocation during a project lock wait and deactivation during a grant write. |

## Subject erasure preview — 2026-11-09

The subject erasure preview's one-shot PostgreSQL plan could attempt the JSON
cast on malformed bound input before evaluating its guard. It now validates
the bounded selection through a materialized input before casting. The
restricted `psql` fixture also verifies that malformed input returns an
unresolved report without echoing the selection.

## Signed credential parsing — 2026-11-09

The operational replay verifier accepted correctly signed duplicate or
case-aliased header and claim names. It now uses the same unique lowercase
field check as staff JWTs. The regression signs conflicting algorithm and
subject fields with the fixture key and verifies generic denial. Protected
mobile JWTs and enrollment assertions apply the same check before identity
lookup or nonce consumption. A follow-up found that golang-jwt/jwt/v5 v5.3.1
ignores signed `crit` and `b64` header parameters. Correctly signed assertions
with unsupported critical extensions or `b64:false` and an encoded payload
were accepted by the replay and mobile verifiers. The shared compact JWT format
check now rejects them before identity lookup or nonce consumption. It permits
explicit `b64:true` only with `crit:["b64"]`; the staff key policy continues to
reject all extra headers. Signed red/green tests cover both failures and
noncritical header compatibility. RFC 7515 §4.1.11 and RFC 7797 §§6–7 govern
this policy; no real issuer or device acceptance is implied.

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
| GA3 | P2 — fixed | A send record could use case aliases to overwrite an earlier JSON field in Go's decoder. Admission now checks exact wire names before decoding. Regression tests cover aliases alone and alongside their canonical names. |
| GA4 | P2 — fixed | Mounted mobile lifecycle handlers reduced verified JWT sessions to app/user IDs before waiting for database locks. A removed or reassigned subject could still act as the previous owner. The transaction now checks and locks the exact mapping and rechecks expiry. PostgreSQL HTTP regressions cover enrollment, refresh, logout, owner switch, and state lookup, plus rollback after expiry during a lock wait. |
| GA5 | P2 — fixed | A case alias such as `ID` could override a campaign target's `id` during Go struct decoding. The nested decoder now requires the exact target keys. Scoped draft create and replace return 400 and preserve campaign, target and audit state. |
| GA6 | P2 — fixed | Project and registry mutations could wait on database locks after the HTTP write deadline. The five-second transaction context now reaches connection acquisition, queries, commit and retries. PostgreSQL tests verify that both the internal budget and an earlier caller deadline release the connection without a resource or audit change. |

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
| ER2 | P2 — fixed | Independent device reservations, submission checks and results took the same exclusive campaign lock. At eight worker DB connections, 3,364 of 4,800 observations waited on it in a 20k trial. These readers now share-lock campaign state. Lease writes and finalization still take the exclusive lock. A PostgreSQL regression holds one reservation through a sibling's provider call and checks that a campaign mutation remains blocked. |

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
| UP2 | P2 — fixed | At a 10,439-row outbox backlog, selecting one scoped event joined 20,000 delivery intents and spilled a 9,448 kB sort. The selector now locks the oldest eligible event before reading its intent and application. The shared 20k fixture checks buffer work and temp writes for one selection. See the [load experiment](docs/analysis/delivery-load.md). |

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
| C1 | `internal/middleware/api_key.go` | Fixed. The mounted middleware rejects empty keys and uses `subtle.ConstantTimeCompare`. | Protected deployment also authenticates the gateway peer. |
| C2 | `internal/features/auth/service.go` — `RefreshToken` | **Original finding superseded by existing correct code:** role lookup uses staff.RoleID and ignores the stale token role | M02e real-DB regression with staff ID 700001/current ADMIN/stale VIEWER refresh claim returns ADMIN access. This slice verifies the existing fix; it does not introduce session rotation. |
| C3 | `internal/features/users/device_tokens_handler.go` — `Register` | `user_id` taken from request body; only the shared API key gates the endpoint | Any caller with the API key can register a device for any user |
| C4 | `internal/features/users/device_tokens_handler.go` — `Delete` | No ownership check — deletes any device token by ID | Any caller with the API key can remove any device registration |
| C5 | Protected staff audit | **Fixed locally.** Only an exact mTLS-authenticated gateway peer may supply a single client address. | Deployment firewall and gateway header replacement require target validation. |
| C6 | `internal/features/staff` — administrative authority | **Remediated in M02b, locally verified 2026-11-03:** all seven routes and public service methods require active current-DB SUPER_ADMIN via `staff.manage`; JWT role gate removed from server wiring | [Evidence](docs/analysis/m02b-verification.md): four-role/seven-route matrix, stale JWT/committed demotion, direct-service fail-closed checks. Statement snapshots, not transactional revocation; not deployed. |
| C7 | `internal/features/staff/service.go` — `UpdatePassword` | **Original claim superseded:** actor password confirmation and 8–72-byte reset bounds already existed; M02b preserves them and adds shared current-authority checks plus fail-closed actor lookup | Reset success/rejection and actor lookup failures pass unit tests; real-DB reset and wrong-password tests pass. Migration 000033 now advances the staff authentication generation on password reset. Password creation policy is separate. |
| C8 | `internal/middleware/jwt.go` — `JWTMiddleware` | **Original finding superseded:** middleware checks IsActive before the handler; inactive/deleted accounts are denied | M02a/b current-authority and committed lifecycle tests pass. M42 adds a current database session and generation check to each staff request. |
| C21 | `internal/authz`; notification/segment/member routes and services | **Fixed in code, locally verified 2026-11-02 (M02a):** current DB authority guards cover all 14 routes, nine mutation services and non-null create/PATCH scheduling input; creators are principal-derived | [Verification](docs/analysis/m02a-verification.md): four-role real-DB/API matrix, direct service denials, stale claims/committed demotion and no-write/no-Kafka tests pass. Statement-time authorization is not a lock through mutation; staff administration/session/atomic audit remain follow-up. Not deployed. |

### Runtime Crashes (Panics)

| # | Location | Issue | Impact |
|---|----------|-------|--------|
| C9 | `internal/token/jwt.go` — `ParseAccessToken` | Fixed. Parsing uses typed destination claims and checks the parser result before use. | Malformed, wrong-key and wrong-token-type tests pass. |
| C10 | `internal/token/jwt.go` — `ParseRefreshToken` | Fixed. Parsing into typed refresh claims returns a domain error on malformed input. | Malformed, wrong-key and wrong-token-type tests pass. |
| C11 | `internal/features/notifications/mock_provider.go` — `Send` | Fixed. The log preview is sliced only when `len(preview) > 8` (`mock_provider.go:99`), so short and empty tokens are logged whole instead of panicking. | `TestMockProvider_Send_ShortTokens_NoPanic` is the recorded regression for this finding. The package is unmounted. |

### Data Correctness (High Blast Radius)

| # | Location | Issue | Impact |
|---|----------|-------|--------|
| C12 | `internal/features/notifications/service.go` — `Send` | Publishes the Kafka event but does not transition the notification to `QUEUED` — status remains `DRAFT` | Two consecutive `Send` calls publish two events and trigger two delivery rounds; concurrent processing can double-deliver to every device. Between publish and worker processing, the row remains editable and deletable |
| C13 | `internal/features/notifications/processor.go` — `ProcessSendRequested` | `producer` field is injected but never invoked | The worker never publishes `NotificationDeliveryUpdated` events, contradicting the README. Downstream analytics receives nothing |
| C14 | `internal/features/segments/members_service.go` — `AddMember` | Existence check uses `ListBySegment(..., 0, 1)` — only the first member is inspected | Duplicate membership rows or unique-violation responses (served as 500) for any segment with more than one member |
| C15 | `internal/features/segments/members_service.go` — `RemoveMember` | Assumes `DeleteBySegmentAndUser` returns an error for a missing row | `ErrMemberNotFound` is unreachable; removing a non-member returns 200 "member removed" for a removal that never occurred |
| C16 | `internal/features/users/service.go` — `List` (external_id fast path) | Every repository error is swallowed and replaced with `([]User{}, 0, nil)` | A database failure is indistinguishable from "no such external_id" — clients receive a successful 200 with empty results |
| C17 | `internal/kafka/producer.go` — `PublishDeliveryUpdated` | Fixed. Both keys are built with `strconv.FormatInt` (`producer.go:136` and `producer.go:175`), and no `string(rune(...))` construction remains anywhere in the tree. | The decimal key format is now correct, but the second half of the original consequence still stands for a different reason: the legacy writer's `LeastBytes` balancer ignores key content, so events for one notification can still land on different partitions. See H47. |
| C18 | `internal/features/segments/service.go` — `GetByID`, `Create`, `Update` | `Actor.ID` reads `actor.ID` even when the staff lookup failed | Responses carry `{id: 0, name: ""}` for the creator reference, discarding the correct value held in `seg.CreatedBy` |
| C19 | `internal/features/users/service.go` — `GetUserNotifications` | Paginates over `notification_deliveries` rows (one per device) rather than distinct notifications | A user with N devices over-counts by a factor of N in both the page and the `Total` field |
| C20 | `internal/features/users/service.go` — `UpdateDeviceToken` | Metadata and status are written by two separate repository calls with no transaction | A failure on the second write leaves the token with updated metadata but an unchanged active flag; the client receives an error naming only the second step |

---

## High Findings

User-visible bugs, security weaknesses without immediate exploitability, and behaviours that silently produce incorrect results.

### Error Handling & Response Semantics

| # | Location | Issue |
|---|----------|-------|
| H1 | `internal/features/staff/handler.go` — `Update` | Fixed locally. Staff update exposes email conflicts as 409, including the actual unique-constraint violation. |
| H2 | `internal/features/auth/handler.go` — `RefreshToken` | Fixed on the mounted route. An inactive staff account or invalid session returns 401. Database errors use the generic 500 response. |
| H3 | Feature-handler unknown 500 branches | **Original broad claim superseded:** reviewed current feature-handler 500 branches use literal internal server error; M02a/b authz error tests verify scoped failures | Source review is not complete redaction coverage for all mapped 4xx, DTOs, logs or future branches. See M02 assessment; production error/security review remains required. |
| H4 | `internal/features/notifications/processor.go` | Status-update errors on the `FAILED` and `COMPLETED` paths are discarded with `_ =` |
| H5 | `internal/features/profile/service.go` — `GetProfile`, `UpdateProfile` | Fixed locally. Missing profile reads map to 404. Database failures propagate as internal errors. Profile writes recheck the active session under lock. |
| H6 | Segment lookup error mapping | The mounted scoped lookup converts only `pgx.ErrNoRows` to 404 and propagates other errors. The original finding describes the legacy module. |
| H7 | User lookup error mapping | The mounted scoped lookup converts only `pgx.ErrNoRows` to 404 and propagates other errors. The original finding describes the legacy module. |
| H8 | `internal/features/staff/service.go` — all read methods | Fixed locally. Staff operations distinguish missing rows and invalid roles from database errors. |

### List & Pagination

| # | Location | Issue |
|---|----------|-------|
| H10 | `internal/features/segments/service.go` — `List` | Total uses the unfiltered `Count(ctx)` even when a search is applied — pagination metadata is wrong for every filtered list |
| H11 | `internal/features/staff/service.go` — `List` | Fixed locally. Staff search and its total use the same name/email predicate. |
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
| H33 | `internal/features/profile/service.go` — `UpdateProfile` | Fixed locally in M34. The profile write enforces the staff email uniqueness constraint and maps a collision to HTTP 409. |
| H34 | `internal/features/segments/service.go` — `Create`, `Update` | Advisory name-uniqueness check with the same TOCTOU properties |
| H35 | `internal/features/segments/service.go` — `Delete` | Member-count guard is not atomic with the delete — a concurrent `AddMember` can race past the check |
| H36 | `internal/features/staff/service.go` — `Create`, `Update` | Fixed locally. The staff create, administrative edit and profile paths map the exact staff email constraint to 409. |
| H37 | `internal/features/users/service.go` — `RegisterDeviceToken`, `UpdateDeviceToken` | Advisory push-token-uniqueness check |

### Partial Commits & Stale State

| # | Location | Issue |
|---|----------|-------|
| H38 | Segment updates | The mounted scoped mutation reads the updated row through `RETURNING` and commits it with the audit record. The original finding describes the legacy module. |
| H39 | `internal/features/staff/service.go` — `Update`, `UpdateStatus` | Fixed locally. The mutation, re-fetch, redacted audit and final-active-administrator check share one transaction. A failure rolls back the write. |
| H40 | Campaign updates | The mounted draft replacement checks the campaign and changes the draft, targets and audit in one transaction. The HTTP integration test verifies rollback after an invalid target. |
| H41 | Campaign creation | The mounted scoped creation inserts the campaign, targets and audit in one transaction. Cross-project references fail without partial records. |
| H42 | `internal/features/segments/service.go` — `Create` | Same pattern — no transaction around the parent and target inserts |

### Configuration & Lifecycle

| # | Location | Issue |
|---|----------|-------|
| H43 | `cmd/server/main.go` | **Fixed, locally tested 2026-11-08.** SIGINT/SIGTERM stop acceptance, allow active HTTP work to drain and then cancel overdue requests. The pool closes after handler exit. A hard-stop deadline reports failure when a handler remains unresponsive. See [process lifecycle](docs/specs/process-lifecycle.md). |
| H44 | `internal/features/notifications/module.go` | The Kafka producer created in the module constructor is never closed |
| H45 | Kafka topic provisioning | Fixed. The legacy writer disables automatic topic creation and the protected writer requires pre-provisioned topics. The local replicated cluster denies unauthorized writes. |
| H46 | Kafka durability | Fixed. Both producer paths configure `RequireAll`. The isolated replicated-cluster drill rejects a write when only one replica remains in sync. Target replication and failure domains require verification. |
| H47 | `internal/kafka/producer.go:96` — `NewProducer` | **Reconciled 2026-11-09; open only on the unmounted legacy producer.** `kafka.LeastBytes` does not hash message keys: `LeastBytes.Balance` in pinned kafka-go v0.4.51 `balancer.go:87` picks the lowest-byte partition and uses `len(msg.Key)` only as a byte counter, so the claimed per-notification partition affinity never held. A local six-message probe over three partitions routed the same key to 0/1/2. The mounted scoped transport restores key affinity with `Balancer: &broker.Hash{}` (`internal/kafka/scoped_send.go:113`); `Hash.Balance` (`balancer.go:153`) hashes a present key and round-robins only when it is nil. `NewProducer` is reachable solely from `internal/features/notifications/module.go:121`, and that module is imported by test files alone — no `cmd/` binary mounts it, and `cmd/worker/main.go:28` delegates to `daemon.Process`. The legacy Godoc previously asserted that keys determine partition assignment, that the balancer was therefore inert, and that both publish methods preserve per-notification ordering; those comments were corrected on 2026-11-09, but the `LeastBytes` balancer itself is unchanged. Remounting the legacy module requires the hash balancer and a reviewed mapping transition first; ADR-07 remains proposed. |
| H48 | `internal/kafka/producer.go:93` — `NewProducer`; `notifications/processor.go` — delivery publish loop | **Reconciled 2026-11-09; open only on the unmounted legacy producer.** Synchronous per-delivery publishes inherit kafka-go's defaults because the legacy writer sets neither field: `Writer.batchSize()` returns 100 (`writer.go:811`) and `Writer.batchTimeout()` returns 1s (`writer.go:825`). A serial caller cannot fill its own batch while waiting for an ack, so sparse publishes accumulate batch waits and delay campaign finalization and update propagation. The mounted scoped transport removes the wait with `BatchSize: 1, MaxAttempts: 1` and one-second read/write timeouts (`internal/kafka/scoped_send.go:114`). Reachability matches H47: test-only. Broker latency has still not been benchmarked, so the ADR-03/05 dispatcher bounds remain a design requirement, not a measured result. |

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
| M25 | `internal/features/staff/dto.go` — `UpdateStatusRequest` | Fixed locally. Missing or null is_active returns 400. Explicit false still deactivates an eligible account. |
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
| M41 | `internal/token/jwt.go` | Fixed locally. Both staff token classes bind the subject to staff ID and require the configured issuer, a single purpose-specific audience and a permitted signing key ID. Protected key rotation and expiry are exercised through the login, refresh and access paths. [Staff policy](docs/specs/staff-sessions.md). |
| M42 | `internal/token/jwt.go`, staff auth, migration 000033 | Fixed locally. Single-use refresh rotation detects replay, logout revokes a family, and credential/role/activity changes invalidate its staff generation. [Session contract and recovery](docs/specs/staff-sessions.md). |
| M43 | `internal/features/auth` | **Partially fixed in M02d:** login/refresh use the 64KiB full-body limit with safe 413/400; real-module rejection tests pass locally. Content-Type enforcement remains open for compatibility. |
| M44 | All 16 JSON input handlers, including profile | **Fixed in local code, M02d:** 64KiB small JSON / 1MiB campaign limits, complete bounded reads and single-value decoding. [Evidence](docs/analysis/m02d-verification.md): boundary/direct-handler/real-DB route tests; no rejected mutation or Kafka event. Other abuse controls remain open. |
| M45 | Protected HTTP API | **Fixed locally.** Login, refresh and logout share per-IP and aggregate authentication budgets before reaching the handler. [Gateway contract](docs/specs/m02-gateway.md). |
| M46 | HTTP layer | No CORS configuration for the admin portal |
| M47 | `internal/features/mobile/service.go` — `verifyJWT` | Correctly enforces HMAC signing — but `internal/token` does not apply the same hardening |

### Kafka Client Configuration

| # | Location | Issue |
|---|----------|-------|
| M48 | `internal/kafka/producer.go:94` — `NewProducer` | **Reconciled 2026-11-09; open only on the unmounted legacy producer.** `kafka.TCP` is variadic and never splits CSV: `makeNetAddr` returns a single address when it receives one string (pinned kafka-go v0.4.51 `address.go:9`). The legacy producer passes one string, so a CSV `KAFKA_BROKER` would be dialled as a literal hostname; the Godoc that promised comma-separated seed support was corrected on 2026-11-09, but the single-string call is unchanged. The reader half of this finding is gone: no `[]string{cfg.KafkaBroker}` construction remains in the tree, and `cmd/worker/main.go:28` delegates to `daemon.Process`. The deployed path parses and validates an endpoint slice instead — `deployment.Broker` decodes `"brokers"` as a JSON array with exact wire names (`internal/deployment/transport.go:19`), `ScopedProducerConfig.Validate` requires one to eight unique, space-free `host:port` entries (`internal/kafka/scoped_transport.go:35`), the writer receives `broker.TCP(addresses...)` (`internal/kafka/scoped_send.go:113`) and the admission reader reuses the same validated slice (`internal/dispatch/kafka_admission.go:113` via `internal/daemon/config.go:96`). Three-seed configuration is exercised in the isolated drill (`scripts/kafka-drill/drill_test.go:290`); target-environment multi-broker failover remains unverified. Legacy `cfg.KafkaBroker` survives only in the unmounted notifications module and in worker/scheduler required-field validation. |

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
| L17 | `pkg/response/response.go` | Fixed. The shared response helper encodes before committing headers and emits the generic JSON 500 on invalid data. A failed transport write aborts the response. The access observer records the committed status even when an error response cannot be sent. |
| L18 | `internal/kafka/events.go` | Event structs carry no event ID or schema version |
| L19 | `internal/features/notifications/dto.go` — `NotificationListItem` | `Type` defaults to `"SEGMENT"` on the unfiltered list path — misleading when the actual type is different |
| L20 | `internal/features/segments/service.go` — `Update` | Writes and audits even when no field has changed, producing a no-op audit entry |

---

## Cross-Cutting Patterns

Recurring issues that appear across multiple features. Fixing each pattern once eliminates a class of findings rather than a single instance.

Reconciled 2026-11-09 against current code. Mounting was verified independently: `cmd/server/main.go` composes only `applications`, `auth`, `profile` and `staff` plus the configured installation/receipt routes, and no other `cmd/` binary imports a feature package, so `notifications`, `mobile`, `users` and `segments` are unmounted. Each pattern therefore states whether it still holds in mounted code, because a pattern confined to unmounted packages is a remount prerequisite rather than a live defect.

### 1. Repository Error Collapse

**Partially remediated.** Mounted features distinguish the two cases: `internal/features/staff/service.go:476` returns `ErrStaffNotFound` only for `pgx.ErrNoRows` and wraps everything else so the handler can produce a 500; `profile`, `auth` and `applications` follow the same convention. The collapse survives in unmounted code — `internal/features/notifications/service.go:397` returns `ErrNotificationNotFound` for any `FindByID` error without testing `pgx.ErrNoRows`, so a connectivity failure would be reported to clients as 404.

**Affected now:** unmounted `notifications`, `mobile`, `users`, `segments`.

**Remaining fix:** apply the mounted convention before remounting any legacy feature.

---

### 2. `err.Error()` Disclosure on the Default 500 Branch

**Remediated, verified 2026-11-09.** No 500 path serves `err.Error()`. Every `StatusInternalServerError` response in the tree passes the literal `"internal server error"`, including the mounted features (`internal/features/applications/handler.go:162`, `internal/features/staff/handler.go:197`, `internal/features/profile/handler.go:100`), the legacy feature handlers, and the shared middleware (`internal/authz/http.go:31`, `internal/middleware/jwt.go:76`, `internal/observability/http.go:88`). `response.JSON` substitutes the same literal when marshalling the envelope fails (`pkg/response/response.go:78`). The surviving `err.Error()` calls are confined to mapped domain 4xx statuses (400/401/404/409), which is the intended contract rather than a disclosure.

---

### 3. TOCTOU on Uniqueness Checks

**Partially remediated.** In mounted code the schema constraint is now authoritative and the violation is translated: `internal/features/staff/service.go:516` routes the write error through `staffmutation.EmailConflict`, which recognises the 23505 unique violation raised by the constraint in `db/migrations/000004_create_staff_users.up.sql:8` and yields `ErrEmailAlreadyUsed` (a 409) rather than a 500. Unmounted code still relies on an advisory pre-check with no 23505 translation — `internal/features/users/service.go:580` — so two concurrent requests can both pass the check and one then fails unmapped.

**Affected now:** unmounted `users`, `mobile`, `segments`.

**Remaining fix:** translate the unique violation in the legacy features before remounting them.

---

### 4. Post-Write Re-Fetch Failures Produce 500

**Does not hold in mounted code. Corrected 2026-11-09 — an earlier note in this section claimed the write committed before the re-read, which is wrong.** The mounted entry point is `StaffService.Update` (`internal/features/staff/mutation.go:58`), not the inner `operations.Update`. It routes through `manage` (`mutation.go:33`) into `staffmutation.Run` (`internal/staffmutation/transaction.go:25`), which wraps the whole operation in one `pgx.BeginTxFunc` transaction and rebinds the repositories *and* the audit service to the transaction handle (`bindStaffOperations(q, ...)` at `mutation.go:23`, called from `mutation.go:39`). A failure of the post-write re-read at `internal/features/staff/service.go:522` therefore returns an error out of the transaction body and `BeginTxFunc` rolls the `Update` back: the client receives a 500, nothing is persisted, and there is no orphaned mutation or missing audit row. The audit write is atomic with the mutation for the same reason.

`*StaffService.Update` also shadows the embedded `*operations.Update`, so `internal/features/staff/service.go:470` is reachable only from unit tests that call `newOperations` directly. Atomicity is already regression-tested: `tests/integration/staff_mutation_test.go:132` adds a `CHECK` constraint that rejects `STAFF_UPDATE` audit rows, asserts the request returns 500, then asserts the stored name reverted to its previous value. That test drives the audit-failure path rather than the re-read-failure path, but both roll back for the same reason — any error returned from the transaction body aborts it.

What remains is a robustness nit rather than a correctness defect: a transient re-read failure aborts an update that would otherwise have succeeded, so the client must retry.

**Affected:** unmounted `internal/features/mobile/service.go:520` has the same post-write re-read shape and was not checked for a transaction wrapper. `notifications`, `profile`, `segments` and `users` were not individually re-verified.

**Recommended fix:** none required for mounted staff. Before remounting any legacy feature, confirm its write, re-read and audit are bound inside one transaction.

---

### 5. Five Copies of `getStaffIDFromContext`

**Still holds.** Five definitions of the same extraction remain and no shared helper was extracted. They are not all named alike: `internal/features/profile/context.go:81` exports `GetStaffIDFromContext`, `internal/audit/context.go:23` exports `GetStaffID`, and `internal/features/segments/helpers.go:122`, `internal/features/notifications/handler.go:557` and `internal/features/users/handler.go:545` each define an unexported `getStaffIDFromContext`. All five bodies are identical — a `middleware.UserContextKey` type assertion to `*token.AccessClaims` returning `claims.StaffID` — so a change to the context representation must still be applied in five places, two of them mounted. A search for the unexported name alone finds only three copies.

**Affected:** mounted `profile`, `audit`; unmounted `segments`, `notifications`, `users`.

**Recommended fix:** extract into a small dependency-free helper package (for example `pkg/identity` or `internal/ctxutil`), have all five call sites use it and retire the divergent names in the same change. This is a maintainability hazard rather than a correctness defect.

---

### 6. Silent Pagination Clamping

**Still holds, unchanged.** `internal/features/staff/handler.go:563` accepts a `limit` only when it parses and satisfies `v > 0 && v <= 100`; any other value silently falls back to the default of 10, and an unparseable or non-positive `page` keeps its default. The response echoes the applied `Page` and `Limit` (`staff/handler.go:295`), which the original finding already noted, so a client still cannot distinguish "you asked for 10" from "you asked for 5000 and got 10". No 400 is returned.

**Affected:** mounted `staff` is confirmed. The other originally listed features (`notifications`, `segments`, `users`) are unmounted and were not individually re-verified.

**Recommended fix:** either reject out-of-range values with a 400 or report both the requested and applied limits in the response metadata. Rejecting is the smaller code change but is a client-visible contract change, so `docs/api/` must be updated in the same commit.

---

### 7. Create-Only vs Upsert Divergence for Device Tokens

**Still holds, but only in unmounted code.** `internal/features/mobile/service.go:498` upserts an existing push token while `internal/features/users/service.go:585` returns a conflict, so a client using the two endpoints interchangeably still sees different results for the "device already registered" case. Neither feature is mounted: device enrollment in the running system goes through `applications.RegisterMobileLifecycle` and `internal/installation`, which is a single contract with no competing endpoint. The divergence is therefore a remount prerequisite, not a live inconsistency.

**Affected:** unmounted `mobile`, `users`.

**Recommended fix:** standardise on one behaviour before either feature is remounted. The upsert is generally correct for client-initiated registration, because the mobile client cannot know whether the server has already seen the device.

---

### 8. Nullable Timestamps as `*string` vs `*time.Time`

**Still holds, unmounted only, and documented as deliberate.** `internal/features/users/dto.go:245` (`DeviceTokenResponse.LastSeenAt`) and `internal/features/users/dto.go:353` (`UserNotification.OpenedAt`) are both `*string` with `omitempty`, while the rest of the codebase uses `*time.Time`. The DTO Godoc at `users/dto.go:209` explains the choice as preserving an absent-versus-empty distinction. That rationale does not by itself require `*string`: a `*time.Time` carrying `omitempty` is also omitted when nil. The real difference is that formatting happens in the service layer instead of at marshalling, so this is a convention divergence rather than a latent bug.

**Affected:** unmounted `users`.

**Recommended fix:** settle the convention before any remount — either convert both fields to `*time.Time` and move formatting to the handler, or record `*string` as intentional and apply it consistently. Do not change it while the package is unmounted, since either choice alters a wire contract that no current client consumes.

---

## Remediation Roadmap

### Sprint 1 — Critical

Focus: crashes, security bypasses, data corruption.

- [x] C9, C10, C11 — the three panic paths are closed: typed destination claims with the parser result checked before use in `internal/token/jwt.go`, and a length-guarded preview slice at `mock_provider.go:99`. `TestMockProvider_Send_ShortTokens_NoPanic` is the C11 regression.
- [x] C2 — existing staff.RoleID refresh lookup reverified with a real-DB regression in M02e
- [ ] C3, C4 — add ownership checks to the device-token register and delete endpoints
- [ ] C12 — add the missing `QUEUED` transition and its guard in `NotificationService.Send`
- [ ] C13 — wire the delivery-update producer or remove the field and the README claim
- [ ] C14, C15 — replace the defective membership checks with proper existence queries
- [x] C1 — the mounted middleware compares with `subtle.ConstantTimeCompare` behind an explicit empty-header check that fails closed (`internal/middleware/api_key.go:125`)
- [x] C17 — both message keys now use `strconv.FormatInt` and no `string(rune(...))` construction remains. Partition affinity on the legacy path is still absent, for the separate reason recorded in H47

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

- [x] M45 — enforce protected authentication request budgets
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
| 2.2 | 2026-11-09 | Correct the pattern 4 conclusion recorded in 2.1. Reading `operations.Update` in isolation missed that the mounted entry point `StaffService.Update` (`internal/features/staff/mutation.go:58`) shadows it and runs the write, re-read and audit inside one `staffmutation.Run` transaction with tx-bound repositories and audit service, so a failed re-read rolls the mutation back and no committed-without-audit state is reachable. `tests/integration/staff_mutation_test.go:132` already regresses that rollback. Pattern 4 is a robustness nit, not a live correctness defect. No runtime change. |
| 2.1 | 2026-11-09 | Reconcile the Cross-Cutting Patterns section and four stale Sprint 1 rows against current code, recording for each pattern whether it still holds in mounted features or only in the unmounted `notifications`, `mobile`, `users` and `segments` packages. Pattern 2 is fully remediated; patterns 1 and 3 are remediated in mounted code only; patterns 5 and 6 still hold in mounted code; patterns 7 and 8 are unmounted-only. The claim made here that pattern 4 also held in mounted code was wrong and is corrected by 2.2. C9/C10/C11, C1 and C17 were already fixed in code but still carried open checkboxes. No runtime change. |
| 2.0 | 2026-11-09 | Correct the package-level `# Partitioning` doc in `internal/kafka/events.go`, which claimed message keys route related events to the same partition and preserve order. That holds only for `ScopedProducer` (`kafka.Hash`); the legacy `Producer` uses `kafka.LeastBytes` and ignores key content, per H47. The doc now states both behaviors. Comment-only; found by auditing the remaining Kafka/provider comments after 1.9. |
| 1.9 | 2026-11-09 | Correct the legacy `NewProducer` Godoc, which asserted key-based partition affinity, per-notification ordering on both publish methods, an inert balancer and comma-separated seed support. All four claims are disproved by the pinned kafka-go source recorded in H47/M48. Comment-only; the `LeastBytes` balancer and single-string `kafka.TCP` call are unchanged, so the legacy defects remain and the module stays unmounted. |
| 1.8 | 2026-11-09 | Reconcile H47/H48/M48 against pinned kafka-go v0.4.51 source and the mounted scoped transport. Each remediation is satisfied on the mounted path; the residual defects are confined to the unmounted legacy `NewProducer`, whose Godoc still asserts the disproved key-hashing and CSV seed behavior. No runtime change. |
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
