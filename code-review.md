# Notification Panel Backend — Code Review Findings

**Version:** 1.7

**Last Updated:** 2026-11-10

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

## Staging-preparation review — 2026-11-10

| ID | Severity | Finding and remediation |
| --- | --- | --- |
| STG1 | P2 — implemented | OAuth refresh collapsed temporary transport/server failures into permanent credential failures, failing recipients and holding application dispatch. The credential boundary now preserves a sanitized temporary classification; the client returns retryable not-submitted network evidence without FCM I/O. Permanent credential rejection retains its terminal result/hold. Regressions cover refresh timeout, temporary status/body failures and bounded charged retry behavior. |
| STG2 | P2 — implemented | A truncated or oversized HTTP 429 body was classified as uncertain network failure before the known status was considered. An observed 429 now preserves quota rejection, the one-minute retry floor and shared cooldown with or without Retry-After. Non-429 body uncertainty remains conservative. |
| STG3 | P2 — implemented | Mounted campaign delivery history read legacy deliveries while the active worker wrote modern intents. History now reads application/campaign-scoped intents, preserves frozen user attribution across owner switches, returns public installation UUIDs and uses UUID delivery cursors. Decimal read-history cursors remain unchanged. The existing update-identity unique index supports bounded scans; no duplicate index or schema migration is added. API documents and examples match the new contract. |
| STG4 | P2 — implemented | Staff/profile mutation transactions retained the session family but lost access-JWT expiry. They now require and recheck access expiry after locks and before commit, and recheck family expiry without invalidating intentional self-password/role changes. Unit and mounted HTTP regressions cover lock-wait and mutation-time expiry with rollback. |
| STG5 | P2 — implemented | Exact-origin CORS omitted mounted PUT operations, conditional/idempotency request headers and ETag exposure. The explicit allowlists now support those contracts while preserving empty/disallowed-origin denial and rejecting unrelated headers. |
| STG6 | P2 — implemented | Readiness accepted an older schema containing the original three outbox/observation tables but lacking current authentication and dispatch objects. Catalog-based capability checks now require current tables, columns and routines without expanding daemon data grants. Private-probe regressions remove staff/session and dispatch capabilities and check denial/recovery. Explicit migration/grant checks remain deployment requirements. |
| STG7 | P2 — implemented | Routine CI did not require full integration, hardened images or operational fixtures. Every configured event now runs full race-enabled integration/sealed restore, image checks, monitoring and physical PITR alongside the existing gates. An aggregate readiness job requires every dependency to succeed. Hosted execution and requiring that job in repository rules remain external verification, not an automatic production sign-off. |

Local verification of this working tree passed focused and full unit race tests,
build, vet, formatting, module and pinned-skill checks, 57 mounted routes and all
published schema examples. The final full race-enabled integration/sealed restore
run passed migrations 1–40 up/down/up, the PostgreSQL/Kafka package in 218.293 s
and FCM in 3.035 s. The new history, staff/profile expiry and incompatible-schema
regressions passed. All six images passed offline startup; monitoring/dashboard
and encrypted physical PITR drills passed. The transient synthetic burst recorded
64 accepted deliveries, 64 Kafka updates, 66 charged attempts and an empty outbox.
The current vulnerability scan found zero reachable advisories; one imported-package
and three module advisories remain outside analyzed call paths. Workflow YAML,
shell steps and aggregate dependencies passed local checks; actionlint execution
was permission-blocked and the hosted workflow remains unverified.

These fixes do not close target identity, real-device, capacity, operational-owner,
retention/erasure or cutover acceptance gates.

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
| C12 | `internal/features/notifications/service.go` — `Send` | Fixed. `UpdateStatusIfDraft(ctx, id, "QUEUED")` claims the transition (`service.go:1030`) and the `claimed == 0` guard aborts when the row is no longer a draft (`service.go:1034`), so a second `Send` cannot re-publish. | The package is unmounted, so this is a remount prerequisite rather than a live protection. |
| C13 | `internal/features/notifications/processor.go` — `ProcessSendRequested` | Fixed. The injected producer is now invoked once per delivery row (`processor.go:424`). | A publish failure is logged rather than returned (`processor.go:425`), so delivery-update publication remains best-effort. Unmounted. |
| C14 | `internal/features/segments/members_service.go` — `AddMember` | Fixed. The existence check calls `memberRepo.Exists(ctx, segmentID, userID)` (`members_service.go:226`) instead of inspecting only the first row of a page. | The mounted equivalent is `applications/segment_writes.go`, which maps the 23505 `uq_segment_member` violation (`:131`). Unmounted. |
| C15 | `internal/features/segments/members_service.go` — `RemoveMember` | Fixed. An `Exists` guard before the delete (`members_service.go:292`) makes `ErrMemberNotFound` reachable, so removing a non-member no longer reports success. | The mounted equivalent uses `DELETE ... RETURNING`. Unmounted. |
| C16 | `internal/features/users/service.go` — `List` (external_id fast path) | Fixed, verified 2026-11-09. Both repository calls wrap and propagate their errors (`service.go:262` and `:267`), returning `nil, 0, err`; no swallowing fast path remains. | A database failure is no longer indistinguishable from an empty result set. The package is unmounted. |
| C17 | `internal/kafka/producer.go` — `PublishDeliveryUpdated` | Fixed. Both keys are built with `strconv.FormatInt` (`producer.go:136` and `producer.go:175`), and no `string(rune(...))` construction remains anywhere in the tree. | The decimal key format is now correct, but the second half of the original consequence still stands for a different reason: the legacy writer's `LeastBytes` balancer ignores key content, so events for one notification can still land on different partitions. See H47. |
| C18 | `internal/features/segments/service.go` — `GetByID`, `Create`, `Update` | Fixed, verified 2026-11-09. `Actor.ID` is populated from the persisted row in all three methods (`service.go:380` and `:534` from `seg.CreatedBy`, `:716` from `updated.CreatedBy`); the staff lookup now supplies only the display name. | Responses no longer carry `{id: 0, name: ""}` for the creator reference. The package is unmounted. |
| C19 | `internal/features/users/service.go` — `GetUserNotifications` | **Fixed in existing code; the prior open claim was incorrect.** `ListByUser` executes `ListUserNotifications`, whose `DISTINCT ON (n.id)` query emits one representative row per notification (`db/queries/notification_deliveries.sql`); `CountByUser` executes `CountUserNotifications`, which uses `COUNT(DISTINCT notification_id)` in the same file. `GetUserNotifications` uses these matched repository methods at `service.go:1097` and `:1102`. | Page contents and total both count distinct notifications, regardless of how many device deliveries each user has. The users package remains unmounted. |
| C20 | `internal/features/users/service.go` — `UpdateDeviceToken` | Fixed. The method begins a transaction (`service.go:762`), binds a tx-scoped device repository (`service.go:774`) and performs its reads inside that transaction, so the metadata and status writes commit or roll back together. | Unmounted. |

---

## High Findings

User-visible bugs, security weaknesses without immediate exploitability, and behaviours that silently produce incorrect results.

### Error Handling & Response Semantics

| # | Location | Issue |
|---|----------|-------|
| H1 | `internal/features/staff/handler.go` — `Update` | Fixed locally. Staff update exposes email conflicts as 409, including the actual unique-constraint violation. |
| H2 | `internal/features/auth/handler.go` — `RefreshToken` | Fixed on the mounted route. An inactive staff account or invalid session returns 401. Database errors use the generic 500 response. |
| H3 | Feature-handler unknown 500 branches | **Original broad claim superseded:** reviewed current feature-handler 500 branches use literal internal server error; M02a/b authz error tests verify scoped failures | Source review is not complete redaction coverage for all mapped 4xx, DTOs, logs or future branches. See M02 assessment; production error/security review remains required. |
| H4 | `internal/features/notifications/processor.go` | Fixed, verified 2026-11-09. Both status-update calls capture `statusErr` and log it — the `FAILED` path at `processor.go:317` and the `COMPLETED` path at `:329` — and no `_ =` discard remains anywhere in the file. The package is unmounted, so this was a remount prerequisite. |
| H5 | `internal/features/profile/service.go` — `GetProfile`, `UpdateProfile` | Fixed locally. Missing profile reads map to 404. Database failures propagate as internal errors. Profile writes recheck the active session under lock. |
| H6 | Segment lookup error mapping | The mounted scoped lookup converts only `pgx.ErrNoRows` to 404 and propagates other errors. The original finding describes the legacy module. |
| H7 | User lookup error mapping | The mounted scoped lookup converts only `pgx.ErrNoRows` to 404 and propagates other errors. The original finding describes the legacy module. |
| H8 | `internal/features/staff/service.go` — all read methods | Fixed locally. Staff operations distinguish missing rows and invalid roles from database errors. |

### List & Pagination

| # | Location | Issue |
|---|----------|-------|
| H10 | `internal/features/segments/service.go` — `List` | Fixed, verified 2026-11-09. The search path totals with `CountSearch` (`service.go:229`), which applies the same name predicate as the page query, and the unfiltered path uses `Count` only at `:253`. Unmounted. |
| H11 | `internal/features/staff/service.go` — `List` | Fixed locally. Staff search and its total use the same name/email predicate. Verified 2026-11-09: `CountMatching` at `service.go:461` shares the repository predicate, which short-circuits to a plain count only when the search is empty. This is the one row in this group that is mounted. |
| H12 | `internal/features/users/service.go` — `List` | Fixed, verified 2026-11-09. Filters now compose in a single call (`service.go:262` and `:267`) and the query ANDs all three predicates (`db/queries/users.sql:216`), so `external_id` no longer suppresses `keyword` or `status`. Unmounted. |
| H13 | `internal/features/users/service.go` — `List` (external_id path) | Fixed, verified 2026-11-09. The separate external_id fast path is gone; offset is applied at `service.go:260` and the total comes from `CountWithFilters` (`db/queries/users.sql:225`), so pagination is honoured on every path. Unmounted. |
| H14 | `internal/features/notifications/service.go` — `List` | Fixed, verified 2026-11-09. Both nullable timestamps are guarded on `.Valid` before the address is taken (`service.go:279` and `:312`), so a NULL no longer renders as `0001-01-01T00:00:00Z`. Unmounted. |
| H15 | `internal/features/notifications/service.go` — `List` | Fixed, verified 2026-11-09. `SentAt` is read from the row (`service.go:283` and `:316`) and `Type` from `row.TargetType` (`:290` and `:323`); `BROADCAST` survives only as a SQL `COALESCE` fallback (`db/queries/notifications.sql:144`), not as a hardcoded Go value. Unmounted. |
| H16 | `internal/features/segments/service.go` — `List` | Fixed, verified 2026-11-09. Member counts come from one batched `CountBySegmentIDs` call (`service.go:269`) backed by a `= ANY(...)` grouped query (`db/queries/segment_members.sql:106`), replacing the per-segment round trips. Unmounted. |
| H17 | `internal/features/segments/service.go` — `List` | Fixed, verified 2026-11-09. The batched count's error is returned wrapped (`service.go:270`); no discarded-error assignment remains, so a failed count can no longer be reported as zero members. Unmounted. |
| H18 | `internal/features/users/service.go` — `Search`, `ListDeviceTokens`, `GetUserSegments` | Fixed, verified 2026-11-09. All three accept `page`/`limit` and return real totals (`service.go:371`, `:447` and `:1002`); the hard-coded 20/100/100 caps are gone. Unmounted. |

### Provider Contract Divergence

| # | Location | Issue |
|---|----------|-------|
| H19 | `internal/features/notifications/onesignal_provider.go` — `Send` | **Fixed and regression-tested.** Results are preallocated to the input length; locally rejected short tokens keep their input positions (`onesignal_provider.go:448`). `TestOneSignalProviderSend/keeps_invalid_token_positions_in_the_result` verifies cardinality and token alignment. |
| H20 | `internal/features/notifications/onesignal_provider.go` — `Send` | **Fixed and regression-tested.** Non-2xx responses fail forwarded tokens before body decoding (`onesignal_provider.go:533`). The unauthorized-response case is covered by `TestOneSignalProviderSend/rejects_non-success_HTTP_status`. |
| H21 | `internal/features/notifications/onesignal_provider.go` — `Send` | **Fixed and regression-tested.** Batch failures return the full per-token result slice plus a function-level error (`onesignal_provider.go:529`). The 503 case is covered by `TestOneSignalProviderSend/returns_per-token_failures_with_batch_error`. |
| H22 | `internal/features/notifications/onesignal_provider.go` — `Send` | **Provider limitation, not a code defect.** OneSignal returns one message ID per batch; successful results therefore share it (`onesignal_provider.go:576`). Receipt correlation must use `(ProviderMessageID, PushToken)`. Per-device IDs require a provider API that exposes them or one request per device. |
| H23 | `internal/features/notifications/onesignal_provider.go` — `Send` | **Fixed and regression-tested.** A lower `Recipients` count fails the forwarded batch because OneSignal does not identify rejected player IDs (`onesignal_provider.go:561`). The partial-acceptance test verifies the batch is not reported successful. |
| H24 | `internal/features/notifications/fcm_provider.go` — `Send` | **Fixed and regression-tested.** The function returns an error when `totalSuccess == 0` (`fcm_provider.go:367`); `TestFCMProviderSendReportsTotalFailure` covers a batch where every token fails. |
| H25 | `internal/features/notifications/fcm_provider.go` — `Send` | **Fixed and regression-tested.** Sends are chunked and dispatched with `SendAll` (`fcm_provider.go:305`); a 501-token test verifies two calls. In pinned Firebase Admin SDK v4.21.0, `SendAll` is deprecated in favor of `SendEach`; `SendEach` makes one HTTP call per message, so review throughput before changing APIs or upgrading. |
| H26 | `internal/features/notifications/fcm_provider.go` — `Send` | **Fixed and regression-tested.** `click_action` is included only when supplied by the request (`fcm_provider.go:260`); tests cover a caller-provided value and omission when empty. |

### Idempotency & Race Conditions

| # | Location | Issue |
|---|----------|-------|
| H27 | `internal/features/notifications/processor.go` | **Legacy limitation remains.** `alreadyProcessed` retries `PROCESSING` only when no delivery rows exist (`processor.go:481`). A crash after provider acceptance but before the first delivery insert can duplicate a push; a crash after partial inserts causes the retry to skip and can lose remaining devices. The module is unmounted; the modern dispatch path uses per-device attempts and recovery. |
| H28 | `internal/features/notifications/processor.go` | **Fixed in existing code.** Delivery attribution matches the returned `PushToken` to the dispatched token (`processor.go:391`) and records that token's ID, so multiple devices for one user do not collapse onto the first match. The package is unmounted and has no direct processor regression test. |
| H29 | `internal/features/notifications/processor.go` | **Still open in legacy code.** Target, broadcast-user and segment-member reads stop at 10,000/1,000,000 rows (`processor.go:603–647`). Reaching a cap emits a warning but still proceeds with a truncated audience. The module is unmounted; the modern dispatch path uses bounded pages. |
| H30 | `internal/features/mobile/service.go` — `Sync` | **Confirmed in unmounted legacy code.** `Sync` upserts the user before validating `params.Platform` (`service.go:220–225`); invalid platform input can therefore return an error after modifying the user row. The mounted lifecycle uses `internal/installation`. |
| H31 | `internal/features/mobile/service.go` — `registerDeviceToken` | **Confirmed in unmounted legacy code.** `UpdateDeviceTokenFull` does not change `user_id`, so a token synced under a different account remains owned by its previous user (`service.go:422`). The mounted lifecycle uses binding/owner-epoch checks in `internal/installation`. |
| H32 | `internal/features/mobile/service.go` — `registerDeviceToken` | **Confirmed in unmounted legacy code.** The existence check and create are separate operations (`service.go:461` onward). `uq_device_push_token` prevents duplicate rows, but a concurrent loser receives the raw unique violation rather than an idempotent result. The mounted lifecycle uses transactional binding operations. |
| H33 | `internal/features/profile/service.go` — `UpdateProfile` | Fixed locally in M34. The profile write enforces the staff email uniqueness constraint and maps a collision to HTTP 409. |
| H34 | `internal/features/segments/service.go` — `Create`, `Update` | **Confirmed in unmounted legacy code.** Both methods check `ExistsByName` before writing; the schema has only a non-unique name index, so concurrent creates/renames can persist duplicate names. The mounted application path serializes per-project segment writes by locking the application row in a transaction before checking the name. |
| H35 | `internal/features/segments/service.go` — `Delete` | **Confirmed in unmounted legacy code.** The count and delete are separate (`service.go:785`); if an `AddMember` commits in between, the delete's `ON DELETE CASCADE` can remove that newly added membership after the add reported success (`000007_create_segment_members.up.sql`). The mounted path locks the segment row before checking references; the membership foreign key prevents a conflicting add from slipping through that lock. |
| H36 | `internal/features/staff/service.go` — `Create`, `Update` | Fixed locally. The staff create, administrative edit and profile paths map the exact staff email constraint to 409. |
| H37 | `internal/features/users/service.go` — `RegisterDeviceToken`, `UpdateDeviceToken` | **Integrity is database-protected; race handling remains open in unmounted legacy code.** `uq_device_push_token` prevents duplicate tokens, but concurrent advisory checks can race and the losing insert/update returns a raw unique violation rather than the documented duplicate-token conflict. The mounted lifecycle uses `internal/installation`. |

### Partial Commits & Stale State

| # | Location | Issue |
|---|----------|-------|
| H38 | Segment updates | The mounted scoped mutation reads the updated row through `RETURNING` and commits it with the audit record. The original finding describes the legacy module. |
| H39 | `internal/features/staff/service.go` — `Update`, `UpdateStatus` | Fixed locally. The mutation, re-fetch, redacted audit and final-active-administrator check share one transaction. A failure rolls back the write. |
| H40 | Campaign updates | The mounted draft replacement checks the campaign and changes the draft, targets and audit in one transaction. The HTTP integration test verifies rollback after an invalid target. |
| H41 | Campaign creation | The mounted scoped creation inserts the campaign, targets and audit in one transaction. Cross-project references fail without partial records. |
| H42 | `internal/features/notifications/service.go` — `Create` | **Confirmed in unmounted legacy code.** The method inserts a notification and its target rows without one transaction; on a target failure it best-effort deletes the parent and ignores any delete error (`service.go:648–695`). A failed compensation can leave a partial draft/target set. The mounted application campaign creation inserts campaign, targets and audit atomically (H41). |

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
| M43 | Mounted JSON handlers | **Media-type portion fixed locally 2026-11-09:** all mounted JSON writes require one `application/json` Content-Type with optional UTF-8 charset and identity content encoding; unsupported media returns safe 415 before service work. Scoped application handlers already used the strict decoder; auth, staff and profile now share the enforcement. The existing 64KiB/1MiB body caps and safe 400/413 behavior remain. Staff/profile name/email enforce the schema-backed `VARCHAR(255)` character limit. Campaign and template content share byte-limit validation with exact/over and multibyte boundary tests. Other field/item/depth controls, caller compatibility and deployment are unverified. |
| M44 | All 17 JSON input handlers, including profile | **Fixed in local code, M02d:** 64KiB small JSON / 1MiB campaign limits, complete bounded reads and single-value decoding. [Evidence](docs/analysis/m02d-verification.md): boundary/direct-handler/real-DB route tests; no rejected mutation or Kafka event. Other abuse controls remain open. |
| M45 | Protected HTTP API | **Fixed locally.** Login, refresh and logout share per-IP and aggregate authentication budgets before reaching the handler. [Gateway contract](docs/specs/m02-gateway.md). |
| M46 | HTTP layer | **Fixed locally:** optional exact browser-origin allowlist applies to API responses and preflight requests behind gateway authentication/rate limits. The protected server policy uses `browser_origins`; local development uses `CORS_ALLOWED_ORIGINS`. Empty configuration denies cross-origin browser access. This does not authenticate requests, enable cookies, or establish the production portal origin. |
| M47 | `internal/features/mobile/service.go` — `verifyJWT` | **Scoped to the unmounted legacy mobile service.** It accepts the HMAC family instead of pinning HS256 and does not enforce issuer, audience or subject. The comparison in the legacy Godoc was stale: `internal/token` staff parsers now pin HS256 and validate their configured issuer/audience, but do not verify mobile tokens. The mounted mobile lifecycle uses a separate protected identity policy. Keep the legacy gap open if that module is ever reactivated. |

### Kafka Client Configuration

| # | Location | Issue |
|---|----------|-------|
| M48 | `internal/kafka/producer.go:94` — `NewProducer` | **Reconciled 2026-11-09; open only on the unmounted legacy producer.** `kafka.TCP` is variadic and never splits CSV: `makeNetAddr` returns a single address when it receives one string (pinned kafka-go v0.4.51 `address.go:9`). The legacy producer passes one string, so a CSV `KAFKA_BROKER` would be dialled as a literal hostname; the Godoc that promised comma-separated seed support was corrected on 2026-11-09, but the single-string call is unchanged. The reader half of this finding is gone: no `[]string{cfg.KafkaBroker}` construction remains in the tree, and `cmd/worker/main.go:28` delegates to `daemon.Process`. The deployed path parses and validates an endpoint slice instead — `deployment.Broker` decodes `"brokers"` as a JSON array with exact wire names (`internal/deployment/transport.go:19`), `ScopedProducerConfig.Validate` requires one to eight unique, space-free `host:port` entries (`internal/kafka/scoped_transport.go:35`), the writer receives `broker.TCP(addresses...)` (`internal/kafka/scoped_send.go:113`) and the admission reader reuses the same validated slice (`internal/dispatch/kafka_admission.go:113` via `internal/daemon/config.go:96`). Three-seed configuration is exercised in the isolated drill (`scripts/kafka-drill/drill_test.go:290`); target-environment multi-broker failover remains unverified. Legacy `cfg.KafkaBroker` survives only in the unmounted notifications module and in worker/scheduler required-field validation. |

---

## Low Findings

Cosmetic issues, minor divergences, and code smells.

| # | Location | Issue |
|---|----------|-------|
| L1 | `internal/audit/constants.go` | Still open, and deliberately constrained. The Go identifiers use `SCREAMING_SNAKE_CASE` — for example `ACTION_STAFF_UPDATE` at `constants.go:17` — which is non-idiomatic. The string values (`"STAFF_UPDATE"`) are persisted audit labels and remain contracts, so only the identifiers may be renamed. Such a rename is safe but is mechanical churn across every call site for a cosmetic gain, and the project profile explicitly keeps persisted labels as contracts even where their naming differs from Go convention. |
| L2 | `internal/token/jwt.go` | `StaffID` serialises as `json:"user_id"` — field name does not match tag |
| L3 | `internal/bootstrap/bootstrap.go` | `"SUPER_ADMIN"` is hardcoded instead of using `cfg.SuperAdminRole` |
| L4 | `internal/bootstrap/bootstrap.go` | No transaction wrapping the role lookup and staff insert |
| L5 | `internal/database/db.go` | No connection pool sizing configuration |
| L6 | `internal/config/scheduler.config.go` | `SchedulerInterval` is the only field with a non-empty default — asymmetric with the rest of the config surface |
| L7 | `internal/config/base.config.go` — `GetDatabaseURL` | **Fixed.** DB credentials, database names and SSL mode are encoded as separate URL components. Tests round-trip reserved characters and IPv6 hosts. |
| L8 | `README.md` | Fixed, verified 2026-11-09. Both claimed versions now match what the repository actually pins: `README.md:5` and `:215` say Go 1.26.10, matching the `go 1.26.10` directive in `go.mod`, and `README.md:76` says Kafka 4.3.1, matching the digest-locked `apache/kafka:4.3.1` image in `compose.integration.yml:30` and `compose.kafka-drill.yml:2`. The integration suite runs that image successfully, which is repository evidence the version exists and works. |
| L9 | `README.md` | Resolved, verified 2026-11-09. The README no longer mentions `NotificationDeliveryUpdated` anywhere, so the claim this finding contradicted is gone. The underlying behavior was also fixed independently: see C13, where the producer is now invoked per delivery row. |
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

**Affected now:** unmounted `notifications` and `mobile`, which contain no `pgx.ErrNoRows` check anywhere in the package, so every lookup failure collapses into a not-found sentinel. `users` (29 such checks) and `segments` (9) do discriminate in general and were wrongly listed here before; individual call sites in those two packages were not exhaustively verified.

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

**Fixed locally in mounted staff.** Omitted parameters still use page 1 / limit 10. Malformed, repeated, non-positive, over-limit and offset-overflowing values now return 400 `invalid pagination`. The service independently validates direct calls and computes the offset in 64-bit arithmetic before converting to the repository's int32 offset. API docs and OpenAPI now describe the rejection contract; tests cover defaults, bounds, duplicates, overflow and the handler response.

**Affected:** mounted `staff` is fixed. The other originally listed features (`notifications`, `segments`, `users`) remain unmounted and were not individually re-verified; revisit their pagination behavior before remounting them.

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
- [x] C3, C4 — identity is derived from the verified mobile JWT (`users/device_tokens_handler.go:218`) rather than a client-supplied user ID, and `ensureDeviceTokenOwnership` compares the token's owner before both update and delete (`users/service.go:943`)
- [x] C12 — `UpdateStatusIfDraft(ctx, id, "QUEUED")` claims the transition and the `claimed == 0` guard aborts it (`notifications/service.go:1030` and `:1034`)
- [x] C13 — the delivery-update producer is wired per delivery row (`notifications/processor.go:424`). A publish failure is logged rather than returned, so it stays non-fatal
- [x] C14, C15 — membership now runs `memberRepo.Exists` before both add and remove (`segments/members_service.go:226` and `:292`), which makes `ErrMemberNotFound` reachable

All ten Sprint 1 items are now closed in code. C3, C4, C12, C13, C14 and C15 all sit in packages imported only by test files, so they are remount prerequisites rather than live protections; the mounted equivalent of the segment membership semantics is `internal/features/applications/segment_writes.go`, which locks the segment before mutating it (`:123`), maps the 23505 `uq_segment_member` violation (`:131`) and uses `DELETE ... RETURNING`.
- [x] C1 — the mounted middleware compares with `subtle.ConstantTimeCompare` behind an explicit empty-header check that fails closed (`internal/middleware/api_key.go:125`)
- [x] C17 — both message keys now use `strconv.FormatInt` and no `string(rune(...))` construction remains. Partition affinity on the legacy path is still absent, for the separate reason recorded in H47

### Sprint 2 — High

Focus: user-visible bugs and contract divergences.

- [x] H1–H8 — the error-mapping gaps are closed and the 500 policy is consistent. No 500 path serves `err.Error()`; H1/H2/H5/H6/H7/H8 map only `pgx.ErrNoRows` (or a real 23505) to their sentinels and propagate everything else wrapped. H3's broad claim was already superseded, and H4 — the last row still written as open — now logs its `statusErr` on both status paths
- [x] H10–H18 — totals, pagination and result caps are all corrected. Only H11 (staff `List`) is mounted and it was already reconciled; H10 and H12–H18 sit in unmounted `segments`, `users` and `notifications`. Mounted staff now rejects invalid pagination and service calls validate the offset; pattern 6 is fixed locally.
- [x] H19–H26 — provider result cardinality, error signaling, partial acceptance and FCM configuration are corrected and focused package tests pass. H22 remains a documented OneSignal batch-ID limitation; pinned SDK SendAll deprecation is a future-upgrade consideration.
- [ ] H27, H29–H32, H34–H35, H37 — remaining findings are confined to unmounted legacy paths and must be fixed or retired before remount. H28, H33 and H36 are fixed in existing code; H35's exact `ON DELETE CASCADE` outcome is recorded above.
- [x] H38–H41 — mounted campaign mutations, campaign creation and staff changes use transactions for their critical writes and audit evidence
- [ ] H42 — the old notification feature still creates the parent and targets with best-effort compensation; it is unmounted and must be fixed or retired before remount
- [x] C19 — notification history already pages and counts per-notification in `ListUserNotifications` / `CountUserNotifications`; the previous open claim was based on an incorrect reading of the repository queries
- [x] C20 — the device-token update now runs inside a transaction with tx-bound reads (`users/service.go:762`)

### Sprint 3 — Medium

Focus: design consistency and maintainability.

- [ ] M1–M8 — consolidate the duplicated helpers and DTOs
- [ ] M9–M14 — remove dead code
- [ ] M15–M27 — reconcile the divergent contracts and naming conventions
- [x] M43 (body cap and mounted JSON media type), M44 — bounded full-body reads for all 17 JSON inputs; media-type behavior is locally tested, not deployed
- [ ] M43 — remaining request/abuse controls beyond mounted JSON media-type enforcement and body limits
- [x] H43 — graceful HTTP shutdown, forced cancellation and bounded handler join
- [x] M35–M37 — migrate to structured logging with a request ID

### Sprint 4 — Polish

Focus: operational readiness.

- [x] M45 — enforce protected authentication request budgets
- [x] M46 — exact-origin CORS policy is configurable; production portal origin and browser acceptance remain deployment gates
- [ ] M38 — Prometheus metrics, the private dashboard and the local alert rules are implemented; OpenTelemetry tracing is genuinely absent (no `otel` import anywhere in the tree) and target alert routing remains open
- [ ] L1 — rename the audit constant identifiers to `MixedCaps`. The persisted string values are contracts and must not change, so this is safe but cosmetic
- [x] L8, L9 — the README's Go and Kafka versions now match `go.mod` and the digest-locked compose images, and the `NotificationDeliveryUpdated` claim that L9 cited is gone from the README

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

The mounted application campaign, segment and staff mutation paths use explicit
transactions for their critical writes and audit evidence. The unmounted legacy
notification creation and mobile sync paths still need transactional fixes or
retirement before remount; do not treat their partial-commit behavior as a live
mounted defect.

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
The old summary undercounted High by one. Reconciliation of the original
closed/fixed rows is in progress: C1, C9, C10, C11, C17, H47, H48, M48 and all
eight cross-cutting patterns were re-verified against current code on 2026-11-09
(changelog rows 1.8 to 2.2). The remaining original rows are unreconciled.

**Cross-cutting patterns:** 8

**Packages affected:** the audit spanned the feature modules and `cmd/` binaries
present when it was written. The tree has since grown to 8 feature modules
(`applications`, `auth`, `mobile`, `notifications`, `profile`, `segments`,
`staff`, `users`) and 6 `cmd/` binaries (`bootstrap`, `requeue`, `retention`,
`scheduler`, `server`, `worker`), so the recorded "7 feature modules, 3 `cmd/`
binaries" scope no longer describes the repository. Of the 8 feature modules only
`applications`, `auth`, `profile` and `staff` are mounted by `cmd/server`. Other
affected packages: `internal/audit`, `internal/config`, `internal/database`,
`internal/kafka`, `internal/middleware`, `internal/token`, `pkg/response`.

---

## Changelog

| Version | Date | Description |
|---------|------|-------------|
| 2.9 | 2026-11-09 | Fix mounted staff pagination clamping and offset overflow: reject invalid/duplicate values with 400, validate direct service calls, and update API/OpenAPI contracts with boundary tests. |
| 2.8 | 2026-11-09 | Share campaign/template title and body byte validation and add exact/over UTF-8 boundary tests; the API contract already specified 255/4096 UTF-8 bytes. The mounted application package passes. |
| 2.7 | 2026-11-09 | Enforce the staff name/email `VARCHAR(255)` character boundary in staff and profile services; document the OpenAPI/API limit and test multibyte Unicode boundaries plus rejection before staff persistence. Target deployment remains unverified. |
| 2.6 | 2026-11-09 | Extend M43 media-type enforcement from auth to all mounted JSON write handlers; profile/staff now return safe 415 for missing, wrong, duplicate or compressed media before service work. OpenAPI/API docs and direct-handler tests updated. Full unit suite and isolated integration suite pass; caller compatibility and deployment remain open. |
| 1.0 | 2026-09-14 | Initial findings inventory compiled from the full-codebase Godoc pass |
| 1.1 | 2026-11-02 | Add open Kafka findings H47/H48/M48 from source/dependency review and key-partition probe; see `docs/analysis/kafka-readiness.md`. No runtime fix applied. |
| 1.2 | 2026-11-02 | Add open role-permission finding C21 from route/service/seed review and recalculate summary counts from distinct IDs; see `docs/analysis/api-data-security-readiness.md`. No runtime fix applied. |
| 2.5 | 2026-11-09 | Re-verify H10–H18; all nine are fixed in code. H11 was already reconciled and is the only mounted row in the group. H10 and H12–H18 sit in unmounted `segments`, `users` and `notifications`: filtered totals now use `CountSearch`, list filters compose in a single query, pagination is honoured on every path, nullable timestamps are guarded on `.Valid`, `SentAt` and `Type` are read from the row rather than hardcoded, member counts are batched with their error returned, and the hard-coded result caps are gone. Clamping an out-of-range `limit` in mounted staff remains and is tracked as cross-cutting pattern 6. No runtime change. |
| 2.4 | 2026-11-09 | Re-verify C16, C18 and H1–H8; all ten are fixed in code. H1, H2, H3, H5, H6, H7 and H8 were already recorded as reconciled, so H4 was the only row still written as an open defect — it now captures and logs `statusErr` on both the `FAILED` and `COMPLETED` paths with no `_ =` discard left in the file. C16 propagates both repository errors instead of swallowing them, and C18 populates `Actor.ID` from the persisted row in all three segment methods. None of the newly closed rows is a live defect in mounted code: C16, C18 and H4 sit in unmounted packages and the mounted H rows were already reconciled. No runtime change. |
| 2.3 | 2026-11-09 | Close the remaining Sprint 1 rows. C3, C4, C12, C13, C14 and C15 are all fixed in code, which with C1, C9, C10, C11 and C17 closes all ten Sprint 1 criticals; all six sit in packages imported only by test files, so they are remount prerequisites rather than live protections. C20 is fixed the same way and C19 remains open in unmounted `users`. Also narrow pattern 1's affected list: `notifications` and `mobile` contain no `pgx.ErrNoRows` check anywhere, while `users` (29 checks) and `segments` (9) were wrongly included. No runtime change. |
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
