# Backend production readiness plan

Date: 2026-11-02 (Asia/Jakarta). Status: active design, not yet production-ready.

This document indexes the latest work and requirements. ADRs remain
`proposed` until decisions and reviews are complete. Implementation checklist items
must not be considered complete merely because a design or code file exists.

## 1. Confirmed requirements

| ID | Requirement | Status / consequences |
| --- | --- | --- |
| R1 | This repository contains only the backend; the admin portal is in another repository | Confirmed by the user. API contracts are in scope; UI implementation is not. |
| R2 | Approximately 20,000 active devices in the initial context | Confirmed by the user; this is not validated per-application capacity or total multi-application capacity. The number of applications/aggregate peak is undetermined. |
| R3 | Approximately 5 campaigns per month | Confirmed by the user; not 100 campaigns/second. |
| R4 | The largest broadcast reaches all active users in the selected application | With the multi-application requirement, broadcasts must not cross application boundaries; fan-out counts eligible devices within that scope. |
| R5 | OneSignal will be replaced by FCM | Confirmed by the user; a permanent OneSignal fallback is not required. |
| R6 | Android and iOS are supported | Confirmed by the user. |
| R7 | The backend sends through FCM for both platforms; iOS uses APNs on the Firebase side | Confirmed by the user. A direct backend APNs provider is not required for the first version. |
| R8 | Broadcasts should be very fast | Confirmed as a requirement direction; no numeric time limit has been agreed. |
| R9 | The mobile application uses React Native | Confirmed by the user; framework/native/library versions have not been verified. |
| R10 | Kafka will be deployed on the company's own servers (self-hosted) | Confirmed by the user; host/failure-domain/resource counts and operators are unknown. |
| R11 | V1 focuses on broadcast/segment/individual, scheduling, recovery, receipts/statistics, security, and operations | Confirmed by the user; UPLOAD, cancel/resubmit, rich media, and preferences/quiet hours are deferred. |
| R12 | VIEWER is read-only; OPERATOR handles drafts/segments; only ADMIN/SUPER_ADMIN may send/schedule; only SUPER_ADMIN may administer staff/roles | Confirmed by the user on 2026-11-02. SPEC M02a is ready for the notification/segment/member subset; M02a guards are implemented/locally tested; M02b staff administration is also implemented/locally verified; other security work remains pending. |
| R13 | A generic backend for multiple mobile applications, not specific to kaiapps2 | Confirmed by the user on 2026-11-03. [ADR-12](../adr/adr-12-multi-application-isolation.md) and the [M03 plan](../specs/m03a-application-scope-plan.md) are proposed; the current runtime/schema does not yet isolate applications. |

The user expects a Firebase project already exists because it is used by the
OneSignal integration. This is a starting point for inventory, **not** confirmation that the project ID,
access, Android/iOS apps, APNs, or backend sending permissions are ready.

Older documents remain useful as history, but assumptions of 100 campaigns/second,
bursts of 1,000/second, a pool of 32, and an estimated 8.6 million idempotency keys/day are not
current requirements. Configuration must be calibrated against device fan-out
and provider quotas, rather than copied from those assumptions.

If each of the five campaigns reaches 20,000 devices, the baseline
estimate is approximately 100,000 device deliveries/month before retries. This is
a scenario calculation, not a measurement of production traffic.

## 2. Initial scope and targets still under proposal

Confirmed v1 scope: auth/staff/profile, user/device lifecycle, segments, campaigns
with broadcast/segment/individual targeting, scheduling, delivery ledger, idempotency,
retry/recovery, receipts, statistics, security, and production operations.

UPLOAD audiences, campaign cancel/resubmit, rich media, quiet hours, and
preferences are deferred from v1. Localisation is undecided; do not promise
OneSignal parity. Contracts must explicitly reject inputs for unsupported
features. Emergency dispatch hold, dedup/idempotency, token/logout lifecycle,
rate control, and audit remain core reliability/security requirements, rather than extra
parity features. A hold stops new claims; it does not recall pushes
already submitted to the provider.

The user states that the React Native repository is available (location not yet provided).
Laravel runs directly on the server and its repository is unavailable.
Auth contract verification remains open; do not assume Laravel server
inspection/deployment has been authorized or make server access a prerequisite
for starting backend baseline fixes.

Initial benchmark proposal (not an SLA and not approved): fan out to 20,000
eligible devices within <=60 seconds from durable queue acceptance until all provider
submission results are recorded, while the system and provider are healthy. Permanent token
failures count as explicit results; retries for provider disruptions are measured
separately. This requires an average of approximately 334 device results/second.

These numbers are only experimental candidates. Local load tests measure the pipeline
against a simulated provider; real-provider tests use only test devices with
appropriate quotas and traffic controls. Simulation results do not establish
provider capacity or arrival times on phones. Set final targets after
baseline benchmarks and the pilot; avoid uncontrolled traffic spikes.

| Open decision | What must be determined | Blocks |
| --- | --- | --- |
| API, scheduling, fan-out, availability SLOs | p95/p99 values, measurement windows, conditions, and error budgets | Final sizing and production go/no-go |
| Infrastructure | Self-hosted Kafka is confirmed; hosts/failure domains, PostgreSQL, resource budget, staging, and TLS are undetermined | ADR-07/08/11 and deployment |
| Recovery | RPO/RTO, backups, restore, operators, and alert escalation | Release gate |
| Mobile | React Native is confirmed; versions/libraries, repository access, update schedule, rollout adoption, and actual identifiers are unverified | ADR-06 and cutover |
| Token lifecycle | Login/logout/account switch, multi-device, refresh, and invalidation | Mobile contract and security tests |
| Retention | Delivery, audit, dead-letter, keys, outbox, PII deletion | ADR-10 and production maintenance |
| Additional scope | UPLOAD, cancel/resubmit, rich media, preferences/quiet hours are deferred; localisation is undecided | Input rejection and API contract boundaries |

## 3. Working tree inventory before this work

No existing changes were discarded, unstaged, or claimed as results of
this session. The Git index already contained staged changes before work began.
Existing processor changes were recorded in commit `bcb178c`, and the old
agent configuration (OpenCode, no longer used) in `51e682c`. The build passed; notifications/middleware/token tests passed,
while staff/segments tests still failed to compile as in the baseline. These
preservation commits do not mean the code meets the new reliability design.

| Group | Files | Initial state and follow-up |
| --- | --- | --- |
| Runtime code | `internal/features/notifications/processor.go` | Staged; changes to PROCESSING guard behavior, token attribution, and warning truncation. Requires a separate review; not equivalent to full ADR-01 implementation. |
| Old drafts | `docs/adr/bigpickle-retry.md`, `docs/specs/bigpickle-retry.md` | Staged with unstaged changes. Retain as historical input; the old SPEC is not the canonical companion to the latest ADR-01. |
| Assessment | `docs/analysis/qwen38-assessment.md` | Staged; several capacity assumptions and safety claims need reconciliation. |
| Canonical ADRs | `docs/adr/adr-01-provider-retry.md` through `adr-05-receipt-ingestion.md` | Untracked, all proposed. Canonical companion SPECs are not yet available. |
| Tooling | `.opencode/agents/{architect,reviewer,tester}.md`, `opencode.json` | Staged; workflow configuration, not backend features. |

Baseline verification was performed during the previous assessment turn in this chat:
`go build ./...` passed; `go test ./...` failed to compile in staff (old
UpdatePassword signature) and segments (CountSearch/CountBySegmentIDs mocks not yet
available). Notifications, middleware, and token passed. The cache used
`GOCACHE=/tmp/hello-go-review-cache`. These are results for the working tree at assessment time,
not evidence that the entire pipeline or clean HEAD passed. This is the historical
baseline before test infrastructure implementation: step 17 later made
the unit suite green, and step 19 ran real PostgreSQL/Kafka dependency smoke tests
and the SDK against an HTTP simulation. Real providers/devices have not been tested.

## 4. ADR / SPEC index

Table paths are relative to `docs/adr/`; canonical companion SPECs will
use the same basenames in `docs/specs/` and have not yet been created.

| ADR | Document | Status and next work |
| --- | --- | --- |
| 01 | [Provider retry](../adr/adr-01-provider-retry.md) | Proposed, revised: atomic fan-out, per-device budget, lease/generation, recovery, and acceptance idempotency. Not yet tested. |
| 02 | [Worker consumption](../adr/adr-02-worker-consumption.md) | Proposed, revised: serial durable admission, fail-closed commit/DLQ, replay audit. Not yet tested. |
| 03 | [Transactional outbox](../adr/adr-03-transactional-outbox.md) | Proposed, revised: one acceptance transaction and a scheduler-only publisher, one row per bounded transaction. Not yet tested. |
| 04 | [Provider selection](../adr/adr-04-provider-selection.md) | Proposed, revised: explicit FCM, credential modes, typed registration, immutable binding/owner_epoch, and SDK limits. Not yet tested. |
| 05 | [Receipt ingestion](../adr/adr-05-receipt-ingestion.md) | Proposed, revised: device-scoped capability receipts, early/late observations, snapshot stats, and versioned event outbox. Not yet tested. |
| 06 | [Cutover/token migration](../adr/adr-06-onesignal-fcm-cutover.md) | Proposed: React Native enrollment, exclusive routing/cohorts, full-coverage gate, pilot, stage-specific rollback, and retirement; not yet executed. |
| 07 | [Kafka topology](../adr/adr-07-kafka-topology.md) | Proposed: self-hosted profiles, RF/ISR, explicit hash routing, retention/groups, seed/batch config, provisioning, and ACL; not yet applied. |
| 08 | [Observability](../adr/adr-08-observability.md) | Proposed: private health/readiness, structured logs, metric catalog/freshness, sparse-work alerts, dashboards/runbooks, and verification; not yet applied. |
| 09 | [API conventions](../adr/adr-09-api-consistency.md) | Proposed: safe envelope/errors, state vocabulary, presence-aware PATCH/CAS, pagination/filter, audit/DTO, and compatibility; not yet applied. |
| 10 | [Retention/recovery](../adr/adr-10-retention-recovery.md) | Proposed: safety floors, controlled erasure/archive, backup/PITR, sealed restore, and source epoch; policy/RPO/RTO not yet set. |
| 11 | [Security baseline](../adr/adr-11-security-baseline.md) | Proposed overall; business role matrix confirmed and scoped SPEC M02a written. M02a operation enforcement is implemented/locally tested; session/abuse/key/enrollment and other security work remain pending. |
| 12 | [Multi-application isolation](../adr/adr-12-multi-application-isolation.md) | NEW proposed: applications as identity/data/permission/provider/event/receipt boundaries; not yet implemented. Optional product parity remains in the backlog without an assigned ADR number. |

First review: [ADR-01/02 review](adr-01-02-review.md). Proposed status means
not yet accepted; migration names mentioned in ADRs are design reservations only.
Actual numbers follow implementation order, always with up/down and sqlc
regeneration. Old drafts must not become parallel implementation instructions.

Next review: [ADR-04/05 review](adr-04-05-review.md), including integration
questions, contract changes, and acceptance tests that have not been run.

Migration gate: [cutover readiness](cutover-readiness.md). The Firebase project,
React Native versions/libraries, and provider configuration still need inventory.

Kafka gate: [Kafka readiness](kafka-readiness.md). H47/H48/M48 are open
runtime findings, not fixes completed merely because ADR-07 exists.

Monitoring gate: [observability readiness](observability-readiness.md).
No live endpoints, scrapes, dashboards, or alert notifications have been
installed merely because ADR-08/a checklist exists.

API/data/security gate: [readiness register](api-data-security-readiness.md).
The C21 operation bypass has been remediated and locally verified in [M02a](m02a-verification.md). Retention/RPO/RTO,
identity policy, and contract changes still require decisions/verification.

Cross-ADR review results: [CR1–CR9 and gates G1–G7](adr-01-11-consistency-review.md).
Technical corrections have been written as proposed; core scope is confirmed, but
SLO/TTL, integrations, hosts, security/data policies, and cutover evidence remain open.
Dependency/milestone order is available in [M00–M10](implementation-milestones.md).
Step 16 is complete as a mapping exercise; step 17/M00 restored the unit test baseline.
Step 18 provides [CI checks and local evidence](ci-readiness.md); a hosted run
has not been observed. Step 19 provides a [dependency environment and local evidence](integration-readiness.md).
Step 20 began with [SPEC M02a](../specs/m02a-staff-operation-permissions.md),
[policy JSON](../contracts/staff-permissions.json), and
[API authorization](../api/authorization-api.md). M02a is implemented/locally verified;
[M02b staff administration](m02b-verification.md) is also implemented/locally verified.
[M02c audit redaction](m02c-verification.md) is also implemented/locally verified;
historical production cleanup remains an operator gate. [M02d request limits](m02d-verification.md)
is also implemented/locally verified. The [baseline assessment](m02-baseline-assessment.md)
and [M02e staff JWT](m02e-verification.md) are complete within scope; next is inspection
of the generic multi-application design and M03a registry/scope before installations;
kaiapps2 is only the first test consumer. [SPEC M03a](../specs/m03a-application-registry.md)
and registry OpenAPI have been written. [M03a.1 persistence](m03a1-verification.md)
is implemented/locally tested. [M03a.2 services](m03a2-verification.md) is also
locally verified. [M03a.3 HTTP](m03a3-verification.md) is also implemented/locally verified;
ten registry/grant endpoints are available, PROVISIONING only. Next is
M03b schema/query/API scope and the legacy pipeline before the second app's business data.
M03b.1–M03b.5 are complete in the repository and locally tested through commit `1b02f1f`.
[M03b cutover](../specs/m03b-cutover.md) in the target environment remains open.
Step 20 continues with [M03c](../specs/m03c-installation-binding.md). Migrations
000018–000019 add installation and binding schemas. Address protections and
lifecycle transactions are available. The per-project JWT verifier and identity mapping
000020 are available. The signed origin adapter and replay protections 000021
are available. The [mobile lifecycle API](../api/mobile-installations-api.md) can be
mounted through server policy. Two synthetic Android/iOS HTTP consumers have
been tested with PostgreSQL for JWT, origin, and proof isolation, address conflicts, and
cross-project logout. Inspection of kaiapps2 still shows OneSignal. Real-consumer
verification and per-project deployment policies remain open.
Integration gates and M02 release controls remain open.

M04 acceptance/scheduling is available and passes locally through migration 000022,
the [transaction contract](../specs/m04-durable-acceptance.md), and the
[API](../api/campaign-acceptance-api.md). PostgreSQL tests cover concurrent
requests with the same key, replay during holds, grant revocation, legacy coverage,
audit rollback, promotion, and expiry. The outbox dispatcher library also passes local
Kafka tests, broker unavailability, two dispatchers, seal/epoch fencing, and
ack-before-commit failure. The dispatcher is not yet wired into a binary; M04 still awaits
safe scheduler activation. Deployment remains sealed/held
and does not send to providers.

M05 atomic preparation is also available as a library and passes locally:
a complete snapshot/count/intent in one REPEATABLE READ transaction, event dedup,
full rollback, coverage holds, and expiry without a false audience. A fixture with 20,000
users/devices completes in approximately 4 seconds for the preparation transaction alone;
this is not a production SLA or FCM delivery time. Consumer, quarantine, and
recovery status is described below; binary wiring remains open. See
[SPEC M05](../specs/m05-durable-preparation.md).

Strict envelope decoding and source-aware quarantine storage also pass unit and
local PostgreSQL tests: identical redelivery, conflicting bytes at the same offset, different topic
incarnations, sealed/inactive sources, commit/insert failures, and size
limits. The serial consumer coordinator is available as a library and passes unit/race tests:
no fetching the next message before ack, no ack when evidence fails, bounded
retries, panic sanitization, shutdown, and assignment loss. The generation-bound adapter
also passes local Kafka tests with two partitions/consumers and the real PostgreSQL Preparer,
including the race detector: revocation while preparation is blocked, redelivery without
duplicate intents, and quarantine before offset commit. Incarnation verification uses
an operator manifest because the SDK does not yet expose the topic UUID. Wiring/deployment remain
open; no production sources have been enabled.

Database QUEUED recovery also passes locally without an outbox/Kafka event: a bounded
scan uses the same Preparer, completes snapshots/expiry, respects due
holds and seal/epoch, and preserves deadlines. PROCESSING/lease recovery,
operator requeue, and process wiring are not complete.

The [modern Go audit](modern-go-audit.md) reviewed installation and dispatch
contracts on 2026-11-04. Two deadline defects were fixed and verified with unit/race
and local PostgreSQL/Kafka integration tests. This does not complete step 20 or
approve deployment. The [operator reprocessing contract](../specs/m05-operator-reprocessing.md) is now
written, and [persistence slice 1](m05-replay-persistence-verification.md) adds
request/attempt/control schema and conditional SQLC primitives.
[Request registration](m05-replay-registration-verification.md) now adds pinned
step-up verification, revocable DB grants and atomic request audit, tested locally.
[Dry-run inspection](m05-replay-inspection-verification.md) also checks exact
acceptance/state/holds without writes. Per-position claim/result, broker I/O and
stale-claim cleanup are next,
followed by worker/scheduler wiring and M06 provider execution. Replay is not enabled.

## 5. Execution checklist

`[x]` means complete with evidence; `[ ]` means incomplete. Some
work on unchecked rows has started, as noted.

### Phase 1 — context and targets

- [ ] 01. Review/adopt existing changes and preserve them in separate commits. Inventory and preservation commits are complete; audit/adoption against the new reliability design are not.
- [x] 02. Create an initial index of ADR, SPEC, backlog, and dependency status (this document).
- [x] 03. Lock core v1 scope; additional features mentioned by the user are deferred. Localisation is not a v1 promise; input boundaries must be explicit in SPECs.
- [x] 04. Record user scale requirements: approximately 20,000 devices, 5 campaigns/month, full broadcast. Final sizing awaits benchmarks.
- [ ] 05. Set SLOs, RPO/RTO, and acceptance thresholds. The 60-second benchmark is only a proposal.
- [ ] 06. Verify mobile/Laravel/token integrations. Android+iOS and transport are confirmed; mobile inspection is not.

### Phase 2 — design

- [x] 07. Review and revise ADR-01/02 at the design level. D1-D7 are mapped to decisions and verification cases; cross-ADR/runtime acceptance is pending.
- [x] 08. Review/revise ADR-03 and idempotency + outbox transactions at the design level; still proposed, not yet implemented.
- [x] 09. Review/revise ADR-04 for Android+iOS FCM, credentials, SDK, and token lifecycle at the design level; mobile/security integration is not yet validated.
- [x] 10. Review/revise ADR-05 for device receipts, statistics snapshots, and versioned event ordering at the design level; not yet tested/accepted.
- [x] 11. Write ADR-06 cutover and its evidence checklist: React Native enrollment, exclusive cohorts, coverage, pilot, per-stage rollback, and retirement; still proposed, not executed.
- [x] 12. Write ADR-07 self-hosted Kafka topology/provisioning and its evidence checklist; still proposed, without infrastructure/runtime changes.
- [x] 13. Write ADR-08 observability, signal/alert catalog, and evidence checklist; still proposed, no instrumentation/monitoring installed or tested.
- [x] 14. Write minimum ADR-09/10/11 decisions and the API/data/security readiness register; still proposed, policy/identity/RPO/RTO not approved, without runtime changes.
- [ ] 15. Cross-ADR review: technical CR1–CR9 checks and revisions are complete at the design level; acceptance remains conditional on G2–G7. No ADR is automatically accepted.

### Phase 3 — implementation foundations

- [x] 16. Organize M00–M10 with a dependency graph, outputs, gates, and completion criteria; milestone implementation is not automatically complete.
- [x] 17. Regenerate segment mocks; align staff actor/password/not-found fixtures and filtered/batch count assertions; build and all unit tests passed on 2026-11-02. No service behavior changes or integration/load-test evidence.
- [x] 18. GitHub Actions + local format/modules/build/test/vet/vulnerability scripts; local checks/actionlint pass, security dependencies updated and reachable scan reports zero findings. Main workflow remains; hosted run/ruleset unverified because changes have not been pushed.
- [x] 19. Isolated fixture runner: 15 migrations up/down/up, repositories, both Kafka topics/producer round trips, and Firebase SDK HTTP simulation pass locally; guards/cleanup verified. Not an adapter/worker/new-pipeline test or real FCM/APNs; no hosted integration job yet.
- [ ] 20. Progresses by milestone: SPEC M02a and implementation/18-case evidence are complete within scope; 56 role/route API cases plus negative DB/Kafka tests pass locally. M02b staff administration is also complete within scope with 28 role/route API cases and local lifecycle/demotion tests; M02c audit redaction and SQL cleanup fixtures pass locally. M02d limits 16 JSON inputs; boundary/48 rejection route cases pass locally. M02e tightens staff JWT parsing and the baseline assessment establishes minimum development guards as ready for isolated M03. Historical production cleanup and release controls are incomplete; this is not evidence of complete security/production readiness. M03a.1–3 registry/grants also pass locally; M03b.1–M03b.5 are complete in the repository and pass locally. Target-environment cutover remains open. M03c has schemas, protections, lifecycle transactions, a verifier, origin, and HTTP. M03d has a contract and two synthetic cross-project consumers that pass locally. State recovery and real binding invalidation pass locally. M06 provider-result hooks, real-consumer verification, and deployment policies remain open. Step 20 also covers M04 durable acceptance foundations.

### Phase 4 — delivery pipeline

- [ ] 21. Provider selection/credentials, error contract, chunking, and invalid-token handling.
- [ ] 22. M05 atomic fan-out intents, device snapshots/identity, and unique constraints pass locally, including 20k intents; M06 exclusive provider claims do not yet.
- [ ] 23. Bounded retry/backoff, lease expiry, recovery, TTL/budget terminalization.
- [ ] 24. Decoder, source-aware dead-letter, serial coordinator, and generation-bound adapter pass locally, including Kafka rebalance with two partitions/consumers; manifest/deployment and operator reprocessing are pending.
- [ ] 25. M04 atomic send idempotency + claim + outbox, promotion/expiry, and bounded dispatcher pass locally; the scheduler binary does not yet use durable handoff.
- [ ] 26. Delivery update outbox and consumer dedup/ordering contract.
- [ ] 27. Bounded concurrency, page/chunk memory limits, provider rate limits, and backpressure.
- [ ] 28. Acceptance/audit rollback, ack-before-commit, preparation/quarantine commit failure, coordinator unsafe-offset, and broker rebalance pass locally; provider and other failure windows/deployment are pending.

### Phase 5 — contracts and security

- [ ] 29. Receipts and statistics with explicit status semantics.
- [ ] 30. Mobile token refresh, login/logout/account switch, multi-device, and migration tests.
- [ ] 31. Close in-scope correctness findings; do not assume the old code-review checklist is accurate without verification.
- [ ] 32. Authorization/ownership, JWT claim validation, rate/body limits, trusted proxies, and secrets.
- [ ] 33. API/event contracts and example/error/compatibility tests for frontend and mobile.

### Phase 6 — operations

- [ ] 34. Production images, resource sizing, config validation, deployment/migration procedures.
- [ ] 35. Graceful shutdown/drain and connection lifecycle for all processes.
- [ ] 36. Tested health/readiness, logs, metrics, dashboards, and alerts.
- [ ] 37. Retention/purge for delivery/audit/key/outbox/dead-letter and PII deletion.
- [ ] 38. Backup/restore and outage/requeue/credential-rotation runbooks.
- [ ] 39. Service owners, alert escalation, and operator access.

### Phase 7 — production evidence

- [ ] 40. Staging end-to-end tests with real Android/iOS devices and relevant application states.
- [ ] 41. Load/burst/soak: 20,000 devices and agreed headroom scenarios; record environment, quotas, and results.
- [ ] 42. Failure injection: restart/rebalance, DB/Kafka outage, provider timeout/429/5xx, duplicate/replay.
- [ ] 43. Restore, deploy, rollback, and migration compatibility drills.
- [ ] 44. Security/operational readiness review; all release blockers closed.
- [ ] 45. Gradual pilot with controlled test traffic/devices and success metrics.
- [ ] 46. Evidence-based go/no-go, gradual cutover, and post-release observation.

## 6. Release gates

Production readiness is declared only when scope and numeric targets are set,
all relevant acceptance tests pass, measured capacity meets targets with
headroom, failure recovery leaves no work without a recovery path,
security blockers are closed, restore/rollback are proven, and operators are ready.

Application receipts measure application reports, not proof that all devices
received a push. There is no end-to-end exactly-once claim or guaranteed arrival
at devices within a particular time. Record duplicate windows and their mitigations.

## 7. Primary sources for provider decisions

- [Firebase Apple setup](https://firebase.google.com/docs/cloud-messaging/ios/get-started): iOS integration and APNs configuration.
- [Firebase scaling guidance](https://firebase.google.com/docs/cloud-messaging/scale-fcm): retry/backoff, traffic shaping, and gradual rollout.
- [Firebase token management](https://firebase.google.com/docs/cloud-messaging/manage-tokens): lifecycle and invalid-token handling.

Sources checked on 2026-11-02. No credentials or `.env` contents were
read. Vendor and mobile integrations have not been tested; this document is not a benchmark result.
