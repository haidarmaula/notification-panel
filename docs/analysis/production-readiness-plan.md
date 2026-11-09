# Backend production readiness plan

Updated 2026-11-09 (Asia/Jakarta). Step 23 is complete. The current phase is pre-production acceptance. Production dispatch remains on hold.

The first unchecked roadmap item is Step 05. The project owner approved initial numeric objectives on 2026-11-09; quota/headroom, failure scenarios, operational ownership, feasibility and target-environment evidence remain open. Independent implementation and verification may proceed in parallel. Step 40 remains the staging device gate, and Step 41 capacity evidence informs the Step 05 decisions.

The [cutover evidence register](cutover-readiness.md) records the outstanding device, provider, operations and approval gates. The [capacity report](delivery-load.md) records the measured results for the current authorization path.

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
| R9 | The mobile application uses React Native | Confirmed. The [first client source checkpoint](mobile-source-checkpoint.md) records React Native, Expo and OneSignal versions. Signed build and device checks remain open. |
| R10 | Kafka will be deployed on the company's own servers (self-hosted) | Confirmed by the user; host/failure-domain/resource counts and operators are unknown. |
| R11 | V1 focuses on broadcast/segment/individual, scheduling, recovery, receipts/statistics, security, and operations | Confirmed by the user; UPLOAD, cancel/resubmit, rich media, and preferences/quiet hours are deferred. |
| R12 | VIEWER is read-only; OPERATOR handles drafts/segments; only ADMIN/SUPER_ADMIN may send/schedule; only SUPER_ADMIN may administer staff/roles | Confirmed by the user on 2026-11-02. SPEC M02a is ready for the notification/segment/member subset; M02a guards are implemented/locally tested; M02b staff administration is also implemented/locally verified; other security work remains pending. |
| R13 | A generic backend for multiple mobile applications, not specific to kaiapps2 | Confirmed by the user on 2026-11-03. [ADR-12](../adr/adr-12-multi-application-isolation.md) and the [M03 plan](../specs/m03a-application-scope-plan.md) have locally tested registry, scoped lifecycle and dispatch foundations; production activation remains gated. |

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

## 2. Initial scope and targets; deployment evidence pending

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

The first React Native repository has been [inspected read-only](mobile-source-checkpoint.md).
Laravel runs directly on the server and its repository is unavailable.
Auth contract verification remains open; do not assume Laravel server
inspection/deployment has been authorized or make server access a prerequisite
for starting backend baseline fixes.

Owner-approved initial fan-out objective: record durable provider outcomes for all
20,000 eligible devices within five minutes from queue acceptance while the system
and provider are healthy. Permanent token failures count as explicit terminal
outcomes; retries for provider disruptions are measured separately. The previously
proposed 60-second objective remains a stretch benchmark, not the production SLO.

Local load tests measure the pipeline against a simulated provider; real-provider
tests use only test devices with appropriate quotas and traffic controls. Simulation
results do not establish provider capacity or arrival times on phones. Firebase's
[current scale guidance](https://firebase.google.com/docs/cloud-messaging/scale-fcm)
recommends smoothing traffic and considering a five-minute window. The approved
five-minute objective is for durable provider outcomes, not device receipt or
display; avoid uncontrolled traffic spikes.

### Step 05 decision record

| Decision | What to approve | Evidence available |
| --- | --- | --- |
| Service levels | Staff API availability 99.9% per rolling 30 days (43.2 minutes of error budget); p95 latency ≤300 ms and p99 ≤1 s for valid requests. Server errors and timeouts are bad outcomes; malformed and unauthorized client requests are excluded from the availability denominator. Use synthetic checks during sparse traffic. For scheduled campaigns not under an explicit hold, 99% reach durable queue acceptance within 60 seconds of `scheduled_for` over a rolling 12 months, and none exceed 5 minutes. Report each scheduled campaign because traffic is sparse. | Project owner approved these initial targets on 2026-11-09. ADR-08 defines lifecycle measurement and sparse-traffic treatment. [ADR-08](../adr/adr-08-observability.md#a-6-lifecycle-timing-sparse-traffic-and-slo-evidence) |
| Recovery objectives | RPO ≤5 minutes; RTO ≤4 hours through safe, reconciled resumption; PITR window ≥30 days. | Project owner approved these initial objectives on 2026-11-09. The isolated restore drills verify recovery mechanics, not target objectives. [Recovery runbook](../runbooks/physical-pitr.md) |
| Acceptance thresholds | All 20,000 eligible devices receive a durable provider outcome within 5 minutes under healthy conditions; 60 seconds remains a stretch benchmark. Require three consecutive staging campaigns and a multi-campaign burst to pass with every eligible device accounted for, no cross-application send, and no unexplained missing or duplicate outcomes. Pause on an ownership/isolation violation or sustained throttling. Roll back on a correctness/security violation or two consecutive runs exceeding 5 minutes. | Project owner approved these initial thresholds on 2026-11-09. The local provider alert warns after at least three quota deferrals in two minutes while cooldown remains active, with a healthy fresh DB sample, no global hold, and a 30-second firing delay; this warning does not itself enact or prove a production pause. Local trials provide calibration only. [Load evidence](delivery-load.md) · [Monitoring runbook](../runbooks/monitoring.md#provider-and-receipts) · [Cutover ADR](../adr/adr-06-onesignal-fcm-cutover.md) |

Approval record: the user approved the initial targets above in this conversation
on 2026-11-09 (Asia/Jakarta). This records project-owner approval only; it does
not represent approval by platform/operations owners or target-environment
evidence.

Step 05 remains open pending the deployment project's actual FCM quota and reserved
headroom, platform/operations approval of the local throttling warning and its
pause/escalation action, agreed campaign soak/burst profile, pilot dwell/cohort
criteria, rollback destination, named operational sign-off and staging validation.
Report schedule delays and held campaigns by their explicit status; do not treat
an excluded hold as a successful on-time schedule.

### Step 05 repository evidence preflight (2026-11-09)

The next repository-verifiable slice is an inventory, not target acceptance:

| Decision | Verified in repository | Still requires target or owner evidence |
| --- | --- | --- |
| Service levels | [ADR-08 A-6](../adr/adr-08-observability.md#a-6-lifecycle-timing-sparse-traffic-and-slo-evidence) defines lifecycle timing, sparse-traffic treatment and measurement boundaries; the project owner approved initial values above. HTTP telemetry now has the 300 ms p95 bucket and records canceled requests without a fabricated 200; focused tests pass. | Platform/operations sign-off, selected-deployment feasibility and observed staging SLIs. |
| Recovery | The [physical PITR runbook](../runbooks/physical-pitr.md) records isolated restore and fencing drills; the project owner approved initial RPO/RTO/PITR values above. | Failure scenarios, retained off-host backups, operator/key custody and target restore observations that meet the approved objectives. |
| Acceptance | The [load report](delivery-load.md) records two isolated 20,000-device runs (49.424 s and 51.450 s) and 100,000-device/five-campaign bursts at 400/s and 1,000/s. A 1,000/s rerun now records each campaign at 213.082–214.969 s from queue acceptance to its last durable result; all 100,000 Kafka IDs were verified. The project owner approved the five-minute per-20,000-device objective and initial pass/pause/rollback criteria above. | Actual quota/headroom, operations approval of the local warning's pause/escalation action, approved campaign burst denominator/rate, repeated staging campaigns and soak, cohort criteria, rollback destination, and real Android/iOS observations. |

The [Step 40 staging prerequisites](../runbooks/delivery-worker.md#step-40-staging-prerequisites)
remain the operator setup checklist. Repository fixtures cannot demonstrate
staging identity provisioning, signed client builds, Firebase/APNs access or
real-device behavior. Do not mark Steps 05, 06 or 40 complete on this preflight.

| Open decision | What must be determined | Blocks |
| --- | --- | --- |
| API, scheduling, fan-out, availability SLOs | Initial project-owner targets are recorded in Step 05; platform validation, actual quota/headroom and deployment evidence remain | Final sizing and production go/no-go |
| Infrastructure | Self-hosted Kafka is confirmed; hosts/failure domains, PostgreSQL, resource budget, staging, and TLS are undetermined | ADR-07/08/11 and deployment |
| Recovery | Initial project-owner RPO/RTO/PITR targets are recorded in Step 05; backup custody, restore operators and alert escalation remain | Release gate |
| Mobile | First client source and dependencies are verified at a recorded revision. Signed builds, release schedule, adoption and actual identifiers remain open | ADR-06 and cutover |
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

The decisions below retain their proposed status until the related owners accept the deployment requirements. The evidence column records the current backend boundary.

| ADR | Document | Evidence and open gate |
| --- | --- | --- |
| 01 | [Provider retry](../adr/adr-01-provider-retry.md) | [Worker recovery](../runbooks/delivery-worker.md) and [20,000-device trials](delivery-load.md) exercise per-device charges, deadline/lease fences and terminal results. Real provider quotas and SLO remain open. |
| 02 | [Worker consumption](../adr/adr-02-worker-consumption.md) | [Admission](../specs/m05-kafka-adapter.md) and [replay](../runbooks/operator-replay.md) cover durable admission, numeric checkpoints and gap fencing. Production source and operator trust remain open. |
| 03 | [Transactional outbox](../adr/adr-03-transactional-outbox.md) | The scheduler publishes durable acceptances, and the worker drains ordered updates. [Composition checks](runtime-composition-verification.md) exercise actual protected commands. |
| 04 | [Provider selection](../adr/adr-04-provider-selection.md) | The scoped worker uses the Firebase SDK with protected credentials. [Wire tests](m06-fcm-transport-verification.md) use a simulated provider. Device and IAM acceptance remain open. |
| 05 | [Receipt ingestion](../adr/adr-05-receipt-ingestion.md) | [Receipt and statistics checks](../specs/m07-receipts-statistics.md) and a [v2 reference consumer](../specs/m06-delivery-consumer.md) pass locally. Real client and downstream integration remain open. |
| 06 | [Cutover](../adr/adr-06-onesignal-fcm-cutover.md) | The [mobile checkpoint](mobile-source-checkpoint.md) found OneSignal in the first consumer. Release, consent/coverage, pilot and retirement await [cutover evidence](cutover-readiness.md). |
| 07 | [Kafka topology](../adr/adr-07-kafka-topology.md) | The [six-node drill](../runbooks/kafka-replication-drill.md) covers replicated writes, ACLs, broker and controller failover and checkpoint continuity. Target failure domains and storage remain unverified. |
| 08 | [Observability](../adr/adr-08-observability.md) | Private diagnostics, dashboards and alert firing/recovery were [exercised locally](../runbooks/monitoring.md). Target scrape inventory, receivers and on-call coverage remain open. |
| 09 | [API conventions](../adr/adr-09-api-consistency.md) | [HTTP and Kafka contracts](../contracts/README.md) check mounted routes, permissions, example data and wire schemas. Real frontend acceptance remains open. |
| 10 | [Retention and recovery](../adr/adr-10-retention-recovery.md) | [Bounded retention](../runbooks/retention.md), [sealed database/Kafka reconciliation](../runbooks/sealed-restore.md) and [physical PITR](../runbooks/physical-pitr.md) have isolated evidence. Erasure, archives, backup policy and actual recovery objectives remain open. |
| 11 | [Security](../adr/adr-11-security-baseline.md) | [DB-backed sessions and rotating keys](../specs/staff-sessions.md), [authenticated ingress](../specs/m02-gateway.md) and [key dependencies](../runbooks/credential-rotation.md) have local evidence. Corporate access and secret custody remain open. |
| 12 | [Application isolation](../adr/adr-12-multi-application-isolation.md) | Registry, role grants, bindings, delivery and receipts use scoped IDs. Two synthetic applications exercise isolation. Production migration remains gated by the [ownership runbook](../specs/m03b-cutover.md). |

### Implementation history

The following entries preserve what each milestone verified at the time. The execution checklist below tracks current status.

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
acceptance/state/holds without writes. [Per-position claims](m05-replay-claim-verification.md)
now commit classification/control/audit atomically and withhold execution proof
on uncertain commit. [Publication/result transactions and expired-claim recovery](m05-replay-execution-verification.md)
also pass locally, including real Kafka, unknown commit outcomes and lease expiry.
The [restricted replay command](m05-replay-command-verification.md) now passes
local unit/race and PostgreSQL verified-TLS/Kafka mTLS integration tests. It has
protected fixed-file configuration, pinned trust, current DB authority checks,
live cluster/topic/partition verification, serial safe reports and resource drain.
No production source is enabled. Trusted issuer/grant provisioning, independently
reviewed incarnation, deployment ACLs and backpressure/handoff verification remain
release gates. [Worker/scheduler composition foundations](../specs/m04-m05-runtime-composition.md)
now include authenticated admission, source-bound publication, bounded due-schedule
polling and four joined scheduler tasks, with a composed local TLS pipeline test.
The [verification record](runtime-composition-verification.md) covers this boundary.
Trusted per-process configuration and actual worker/scheduler entry points are
now wired and locally verified. Kafka admission runs in its own worker mode. M06
[lease/reservation foundations](m06-execution-verification.md) now pass focused
local tests. Dedicated [receipt signing and scoped payload libraries](m06-receipt-payload-verification.md)
now also pass local tests. Atomic [capability metadata persistence](m06-receipt-metadata-verification.md)
is now locally tested. The [route-bound FCM transport](m06-fcm-transport-verification.md)
also passes local SDK/HTTP simulation and race tests, including one physical
request per permit. [Protected file credential loading](m06-fcm-credentials-verification.md)
also passes local OAuth/SDK and offline protected-filesystem tests. Deployment
credential selection remains open; managed ADC is conditional on choosing keyless
deployment. [Single-use DB-authorized submission](m06-submission-verification.md)
is implemented with final checks after credential/rate waits.
[Fenced result persistence/outbox insertion](m06-results-verification.md) is implemented.
[Ordered update publication](m06-update-publication-verification.md) passes focused
local Kafka tests. [Execution recovery and campaign finalization](../specs/m06-recovery.md) are implemented. The [delivery worker](../runbooks/delivery-worker.md) polls its configured application. Production
activation and acceptance stay open.

## 5. Execution checklist

`[x]` means complete with evidence; `[ ]` means incomplete. Some
work on unchecked rows has started, as noted.

### Phase 1 — context and targets

- [x] 01. Reviewed the preserved legacy processor and tooling commits. The current worker enters through `internal/daemon`, uses fenced per-device attempts and has no call to the old processor constructor. Historical retry drafts and OpenCode configuration remain reference material. The preserved processor’s post-send history heuristic and target truncation were excluded from the modern path; the 20,000-device fixture verifies the current path.
- [x] 02. Create an initial index of ADR, SPEC, backlog, and dependency status (this document).
- [x] 03. Lock core v1 scope; additional features mentioned by the user are deferred. Localisation is not a v1 promise; input boundaries must be explicit in SPECs.
- [x] 04. Record user scale requirements: approximately 20,000 devices, 5 campaigns/month, full broadcast. Final sizing awaits benchmarks.
- [ ] 05. Set SLOs, RPO/RTO, and acceptance thresholds. Measurement boundaries and required approvals are in the [Step 05 decision record](#step-05-decision-record).
- [ ] 06. Verify mobile/Laravel/token integrations. [First mobile source inspected](mobile-source-checkpoint.md); the current client still uses OneSignal. Laravel issuer, provider project and real device enrollment remain unverified.

### Phase 2 — design

- [x] 07. Review and revise ADR-01/02 at the design level. D1-D7 are mapped to decisions and verification cases; cross-ADR/runtime acceptance is pending.
- [x] 08. Review/revise ADR-03 and idempotency + outbox transactions at the design level. Atomic acceptance, idempotency and outbox publication now have local implementation evidence in Steps 25–26.
- [x] 09. Review/revise ADR-04 for Android+iOS FCM, credentials, SDK, and token lifecycle at the design level; mobile/security integration is not yet validated.
- [x] 10. Review/revise ADR-05 for device receipts, statistics snapshots, and versioned event ordering at the design level. Local HTTP, PostgreSQL and reference-consumer tests now cover the contract in Steps 26 and 29.
- [x] 11. Write ADR-06 cutover and its evidence checklist: React Native enrollment, exclusive cohorts, coverage, pilot, per-stage rollback, and retirement; still proposed, not executed.
- [x] 12. Write ADR-07 self-hosted Kafka topology/provisioning and its evidence checklist; the isolated three-controller/three-broker drill passes. Company host placement and release approval remain open.
- [x] 13. Write ADR-08 observability, signal/alert catalog, and evidence checklist; local monitoring installation, alert rules and dashboard queries now have evidence in Step 36.
- [x] 14. Write minimum ADR-09/10/11 decisions and the API/data/security readiness register; still proposed, policy/identity and target recovery implementation/acceptance remain open. Initial RPO/RTO/PITR objectives were approved by the project owner on 2026-11-09. Local controls and recovery evidence are recorded in Steps 32 and 37–38.
- [ ] 15. Cross-ADR review: technical CR1–CR9 checks and revisions are complete at the design level; acceptance remains conditional on G2–G7. No ADR is automatically accepted.

### Phase 3 — implementation foundations

- [x] 16. Organize M00–M10 with a dependency graph, outputs, gates, and completion criteria; milestone implementation is not automatically complete.
- [x] 17. Regenerate segment mocks; align staff actor/password/not-found fixtures and filtered/batch count assertions; build and all unit tests passed on 2026-11-02. No service behavior changes or integration/load-test evidence.
- [x] 18. GitHub Actions + local format/modules/build/test/vet/vulnerability scripts; local checks/actionlint pass, security dependencies updated and reachable scan reports zero findings. Main workflow remains; hosted run/ruleset unverified because changes have not been pushed.
- [x] 19. The isolated fixture exercises the migration chain in both directions, PostgreSQL repositories, both Kafka topics, the worker, and Firebase SDK traffic against a simulator. CI includes integration and synthetic worker jobs. The protected environment and hosted run still require deployment evidence. See the [integration runbook](integration-readiness.md).
- [ ] 20. Staff operation and lifecycle permissions, input limits, session revocation, project ownership, mobile binding, durable acceptance, fenced provider results and reference consumers have isolated test evidence. The selected deployment still needs verified legacy data and writer cleanup, real consumer compatibility, credentials and release approval.

### Phase 4 — delivery pipeline

- [ ] 21. Scoped FCM transport/errors, file credentials, DB-authorized single-use submission, fenced results and conditional invalid-token handling pass local tests. Selected deployment credential policy and delivery activation remain open.
- [ ] 22. M05 atomic fan-out/device snapshots pass locally, including 20k intents. M06 leases, per-device claims, submission and fenced results also pass locally; recovery and finalization are implemented; target-environment activation remains open.
- [x] 23. Per-device retries use bounded exponential backoff and preserve charged attempts after lease expiry. The worker recovers eligible devices and finalizes deliveries that exhaust their deadline or budget. Verified with PostgreSQL, Kafka and a synthetic FCM endpoint in the [isolated worker deployment](../runbooks/delivery-worker.md#acceptance).
- [ ] 24. Decoder, source-aware dead-letter, serial coordinator, and generation-bound adapter pass locally, including Kafka rebalance with two partitions/consumers; replay registration/inspection/claim/publication and expired-claim cleanup libraries also pass locally. The replay verifier rejects signed duplicate or case-aliased JWT fields. Replay CLI/configuration and PostgreSQL verified-TLS/Kafka mTLS command tests also pass locally. Numeric group checkpoints, live retention gaps and generation handoff also pass locally. A quarantine integration regression confirms runtime sealing and source deactivation take effect after a waiting writer acquires the corresponding row lock. Production issuer/incarnation approval and target runtime deployment remain pending.
- [ ] 25. M04 atomic send idempotency + claim + outbox, promotion/expiry, and bounded dispatcher pass locally; the scheduler binary uses protected source-bound durable handoff. PostgreSQL tests confirm campaign acceptance rolls back when the staff session expires or is revoked while waiting for a lock, and that a blocked scheduled campaign leaves later campaigns eligible for promotion. Production policy/activation and target-environment evidence remain open.
- [ ] 26. Atomic versioned outbox insertion and predecessor-safe publication pass local tests. The delivery worker drains its scoped outbox. A PostgreSQL reference consumer passes duplicate, version gap, conflict, restart and restored-source tests against the published v2 record. Rollout evidence from actual downstream owners remains open. See the [consumer contract](../specs/m06-delivery-consumer.md).
- [x] 27. Bounded pages and provider concurrency, process and fleet allocations, memory measurements and 429 backpressure pass the isolated [load experiments](delivery-load.md).
- [ ] 28. Local tests cover rollback, ambiguous commits, stale owners/offsets, broker rebalance, provider timeout, 429 and 503. The [load fixture](delivery-load.md) checks charges, deferrals and exact update IDs. A PostgreSQL regression verifies that a failed preparation leaves its campaign retryable while later due campaigns continue. Target failure and migration drills remain open.

### Phase 5 — contracts and security

- [x] 29. Scoped mobile receipts and staff campaign statistics are implemented. PostgreSQL and HTTP fixtures verify account and key changes, expiry, duplicate requests, early and late observations, atomic rollback and snapshot counts. See the [M07 contract](../specs/m07-receipts-statistics.md).
- [ ] 30. Backend fixtures cover refresh, logout, account switching, ownership epochs and two-project isolation. The mounted lifecycle rejects a subject remapped or removed during a database lock wait and rolls back work when the session expires. Protected mobile JWTs and signed enrollment assertions reject duplicate or case-aliased fields before identity lookup or nonce consumption. Real app integration and the multi-device migration pilot remain open. See the [source checkpoint](mobile-source-checkpoint.md).
- [ ] 31. The mounted staff and profile paths serialize admin changes, protect the last active super-admin, retain current roles and reject stale or overlong password proofs. Rejected staff logins perform comparable bcrypt work at the default cost for unknown or inactive accounts and invalid stored hashes. The [review inventory](../../code-review.md) records mounted credential handling and distinguishes the protected Kafka writer from the legacy notification module. The mounted mobile lifecycle rechecks subject mapping and expiry in its transaction. Project writes use bounded transactions and recheck the staff session before committing, and grant enables serialize against target deactivation. Target security review remains open. Kafka findings H47/H48/M48 were reconciled on 2026-11-09 against the pinned kafka-go source: the mounted scoped transport satisfies each remediation, and the residual defects are confined to the unmounted legacy `NewProducer`. Other historical rows in the review inventory remain unreconciled.
- [ ] 32. Project and current staff authority, rotating JWT families, exact gateway mTLS/peer addresses, and bounded auth and mobile receipt requests pass local tests. Target identity, rate allocations, gateway and secret custody remain open.
- [x] 33. Published and verified all 57 mounted HTTP method/path pairs and both Kafka wire schemas. Synthetic examples and error cases exercise the real routers, Kafka decoders, campaign acceptance and receipt ingress. [Contracts and verification](../contracts/README.md).

### Phase 6 — operations

- [ ] 34. All six process images use the pinned toolchain and run as a non-root user. The offline image check builds each command and verifies its entrypoint and protected policy behavior on a read-only filesystem. It passed locally on 2026-11-09 for server (`df2b45dc99d0778f5135459b1e42a082aa3a915d6a8a9db279ad23d897f74c39`), bootstrap (`3fe7ae02bc2f5e02f4d5056175e90cb3f52f372c4019c1ce52c8c15d85ab05f8`), worker (`320af60b1aa984e91e95f53cdda8b6411bb936bd68dfcc41c725c8f46aa022bf`), scheduler (`098c1298b48e7cd6a1a002c341be54e14154b5cd3c01c765d59f027b18cfeff5`), requeue (`5e93f98a3c849dabf13d52e8f0ec64a8ec65349706610e507e037f29e641c83f`) and retention (`01bddb219d2b5e55fed816ba1c85a6df0955c85a29ff02a62e792ab036d5ff52`). Protected role configuration is implemented. Target resource sizing, image attestation and deployment drills remain open. See the [image runbook](../runbooks/production-images.md).
- [x] 35. Server, worker, scheduler and one-shot commands now bound startup and join or cancel outstanding work on shutdown. Local tests cover active HTTP drain, forced cancellation, inherited pipes and closing PostgreSQL connections. See the [lifecycle specification](../specs/process-lifecycle.md).
- [ ] 36. Private mTLS health/readiness, redacted structured HTTP logs, provider/outbox metrics, a provisioned Grafana dashboard and Prometheus firing/recovery fixtures are implemented. Delivery workers distinguish quota, breaker and concurrency deferrals. Local rule tests verify quota and breaker alert firing and suppression for idle gates, ordinary concurrency, global holds and stale samples. The local Kafka drill verifies replication, insufficient ISR, offline partition and scrape alerts. The admission collector reports committed checkpoint lag and range validity using its scoped credentials. The isolated drill verifies missing checkpoints, measurable lag, zero lag, group denial and recovery. Prometheus and Alertmanager deliver the firing and resolved offline-partition notifications to an isolated webhook. Target scrape inventory, admission and downstream checkpoints, the on-call route and watchdog checks remain open. See the [monitoring runbook](../runbooks/monitoring.md).
- [ ] 37. A [protected retention job](../runbooks/retention.md) expires spent origin nonces, receipt request markers and reviewed terminal send keys in bounded authorized batches with holds and crash-safe progress. A [read-only subject impact preview](../runbooks/subject-erasure.md) inventories dependent records and incomplete coverage. The PostgreSQL 17 command-line fixture verifies bound selection input over TLS with the restricted reader, including safe unresolved results for malformed selections. Archive behavior, actual erasure across systems and owner-approved lifetimes remain open.
- [ ] 38. [Sealed Kafka reconciliation](../runbooks/sealed-restore.md), [physical PITR and missing-WAL denial](../runbooks/physical-pitr.md), and [credential rollover](../runbooks/credential-rotation.md) pass isolated checks. The physical PITR drill passed locally on 2026-11-09 against PostgreSQL 17.10 at migration 000040: target LSN `0/D5A948` on timeline 1 restored through security reconciliation in 2.200 s; wrong-key and missing-WAL recovery were rejected. Credential-rotation integration regressions also passed in the race-enabled isolated suite. These synthetic fixtures do not establish target RPO/RTO, backup custody, or target credential retirement. Recovery objectives are approved as initial targets; meeting them, backup custody and credential retirement remain open.
- [ ] 39. Operator grants and alert delivery through a synthetic webhook have isolated evidence. The company service owners, on-call receivers, escalation paths and operator access still require verification.

### Phase 7 — production evidence

- [ ] 40. Staging end-to-end tests with real Android/iOS devices and relevant application states. Provision the isolated backend, Firebase and Apple identities, Laravel and origin broker, and signed client builds using the [staging prerequisites](../runbooks/delivery-worker.md#step-40-staging-prerequisites).
- [ ] 41. [Measured synthetic 20,000-device trials, quota holds and multi-campaign bursts](delivery-load.md). Isolated 20,000-device trials recorded all results in 49.424 s and 51.450 s. Five-campaign/100,000-device bursts completed all provider outcomes in 360.177 s at 400/s and 222.242 s at 1,000/s; each verified all Kafka IDs. A new 1,000/s rerun reported each 20,000-device campaign's last durable result in 213.082–214.969 s and verified all 100,000 Kafka IDs. All measurements are local synthetic evidence; target storage, network, provider quota and agreed burst/headroom/soak profile remain open.
- [ ] 42. Local failure drills cover controller/broker elections, lost ISR, monitored full broker loss, gaps, DB rollback, ambiguous publication, expired leases, duplicate/replay and provider timeout/429/503. The three-controller/three-broker drill passed locally on 2026-11-09: controller replacement through verified canaries and quorum took 21 s; broker recovery restored full ISR, canaries and checkpoints in 11 s. Repeat in the approved failure domains with independent monitoring.
- [ ] 43. Logical and physical recovery, post-backup revocations, missing WAL and guarded downgrade refusal pass locally. The [worker rollback drill](../runbooks/sealed-restore.md#worker-rollback-on-the-current-schema) and full race-enabled isolated integration suite passed on 2026-11-09; it builds the reviewed prior worker and verifies it resumes held work on the current schema without duplicate Kafka outcomes or an extra provider send. Target restore, compatible image rollback, off-site recovery keys and cutover rehearsal remain open.
- [ ] 44. Security/operational readiness review; all release blockers closed.
- [ ] 45. The [enrollment switch](../runbooks/application-lifecycle.md), [per-binding routes](../runbooks/binding-route-cutover.md) and [pilot/full release procedure](../runbooks/application-dispatch.md) enforce scoped approval, coverage and holds in the backend. The actual exclusive cohort, signed builds and observed pilot remain open.
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
