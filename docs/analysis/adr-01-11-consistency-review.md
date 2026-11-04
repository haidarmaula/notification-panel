# ADR-01–11 consistency review

Date: 2026-11-02. Status: design review; ADRs remain **proposed**.
No runtime changes, migrations, provisioning, or production evidence.
Requirements index: [production plan](production-readiness-plan.md).

## 1. Scope and acceptance boundaries

The user confirmed that v1 focuses on broadcast/segment/individual, scheduling,
recovery, receipts/statistics, security, and operations. UPLOAD, cancel/resubmit,
rich media, and preferences/quiet hours are deferred. Emergency hold, idempotency,
token/logout lifecycle, traffic limits, and audit are core controls.
Localisation is not a promised v1 feature; the contract must describe supported inputs.

Self-hosted Kafka is confirmed; the number of independent hosts is **unknown**.
The React Native kaiapps2 repository is available at the authorized path and branch
feat/notifications; it is only the first consumer reference, not a platform boundary. The Laravel repository
is unavailable; its application runs directly on the server. Auth verification can
use redacted contracts/configuration and test tokens through the system owner;
it does not require copying production credentials or automatic server access.

This review separates technical corrections that can be designed now from
product/operational choices requiring a decision owner. Not all release gates
must be complete before repairing baseline tests; acceptance of a milestone's SPEC
requires the decisions actually used by that milestone.

## 2. Cross-ADR findings and proposed resolutions

| ID | Conflict / risk | Design resolution | Related ADRs |
| --- | --- | --- | --- |
| CR1 | Idempotency, receipt, registration, and revocation lock order lacks a single protocol | Shared order in section 3; test races and implicit FK locks in the SPEC | 01,03,04,05,09,10,11 |
| CR2 | “Consistent snapshot” does not specify isolation; coverage/count/intent can differ | REPEATABLE READ preparation, one predicate/snapshot, bounded whole-transaction retry | 01,05,06 |
| CR3 | Reserving a whole page consumes budget before breaker/rate permits are available | Candidate page without charge; reserve one device after permit, with attempt ID and fencing | 01,04 |
| CR4 | Breaker counts batches, FCM executor counts devices | One outcome per logical device submission; internal SDK retries are not additional outcomes | 01,04,08 |
| CR5 | Deadline based on actual promotion revives stale campaigns after an outage | Scheduled deadline = scheduled_for + TTL; expired schedule FAILED without false outbox/fanout | 01,03,06,08 |
| CR6 | Late observation on FAILED can be misread as success during campaign aggregation | FAILED remains a submission failure; separate observation axis even while campaign is PROCESSING | 01,05,08 |
| CR7 | Topic name/partition/offset can be reused after topic recreation | Quarantine uniqueness by physical log source incarnation + partition + offset | 02,07,10 |
| CR8 | source_epoch conflates acceptance, recovery, and Kafka source identities | Separate origin_epoch, source_epoch, and log_source_id; section 4 | 02,03,05,07,10 |
| CR9 | Capability expiry based on schedule acceptance can precede sending | Receipt capability expiry = persisted deadline_at + proposed 7d | 01,05,10,11 |
| CR10 | Multiple applications confirmed after initial review; current data/permissions/events are global | [ADR-12](../adr/adr-12-multi-application-isolation.md) requires end-to-end app scope, no default-app fallback, and a legacy query gate before a second app. [M03a–d](../specs/m03a-application-scope-plan.md) plan; no A/B implementation/tests yet | 01–12 |

These resolutions have been incorporated into the related ADRs or the shared protocol here.
“Resolved in design” status does not close runtime findings in `code-review.md`.
Timeout, TTL, breaker, rate/concurrency, and retention values remain proposed.

## 3. Shared transaction and lock protocol

If an operation needs to acquire several groups of row locks, the order is:

1. Control/policy rows that actually need locking, then staff/user principals,
   then session/revocation rows. Use stable table and ID ordering;
   actor/target staff use the same ID order, rather than always actor first.
2. Idempotency key, then notification/campaign rows, ordered by ID.
3. Installation rows, immutable registration/binding rows, then delivery rows;
   each ordered by ID. Attempt/result/finalization acquires campaign
   before delivery; receipts also acquire installation/binding before delivery.
4. Evidence/read/projection, audit, and outbox writes in the same transaction.

Operations lock only the rows they need; this rule does not instruct locking
the entire audience/principal set or one global mutex for every submission.
Cryptographic verification and network I/O are outside transactions; the current
authority/owner/version governing a mutation is rechecked in the transaction.
Campaign renewal does not acquire an installation lock and then return to the campaign.

Registration/logout can use principal → installation → binding without a
campaign. If campaign maintenance is needed, commit revocation first, then perform
the campaign phase separately with a recheck; do not hold an installation
while looking up a campaign. Maintenance discovery uses IDs without child row locks,
then opens a transaction in the order above and rechecks conditions.
A read snapshot does not authorize sending to an owner who has changed;
the executor still checks current registration before every claim.

The outbox dispatcher locks one outbox row and does not go back to acquire its parent
campaign/principal. If an operation requires audit FK/principal locks, acquire the
necessary ones before child locks. The SPEC must check implicit FK locks, unique
conflicts, and triggers, not just written SELECT FOR UPDATE statements. Lock/statement
timeouts and whole-transaction retries for 40001/40P01 are bounded; a diagram alone
does not establish freedom from deadlocks. Provider network calls do not run while
holding a campaign lock; bounded Kafka outbox publication is an explicit
ADR-03/05 exception, not permission for network I/O in other transactions.

Preparation uses REPEATABLE READ for coverage, audience count, and set-wise
intent insertion in the same snapshot. A snapshot is not the entire database
frozen forever: the executor checks later eligibility changes. Completion
markers/counts become visible only after commit. Expiry before preparation may
produce FAILED with an unknown snapshot; only a complete snapshot may produce
COMPLETED or aggregate device outcomes. An empty snapshot is not unknown.

Isolation and retry choices are based on primary PostgreSQL documentation:
[transaction isolation](https://www.postgresql.org/docs/17/transaction-iso.html)
and [explicit locking](https://www.postgresql.org/docs/17/explicit-locking.html).
This is a design basis, not an integration test result on the project's database.

## 4. Identities, versions, and recovery

| Identity | Meaning and invariant |
| --- | --- |
| origin_epoch + notification_id | Immutable business acceptance; existing intents do not gain a new business identity on restore |
| source_epoch | Publication/projection lineage activated through the recovery manifest; stable across ordinary restarts |
| log_source_id + partition + offset | Physical Kafka source location; cluster and topic incarnation must be known |
| delivery_id | Opaque per-device intent identity; unchanged across retries |
| lease_generation + attempt_id | Fence DB results against lease takeover and duplicate device reservations |
| owner_epoch | Installation owner authorization version, changes on logout/account switch |
| route_epoch | Policy/cohort cutover version; not authorization to change an old audience |
| event_id / delivery_version | Global event dedup / monotonic projection version within the approved source lineage |

Send and update envelopes carry origin_epoch and source_epoch. Admission
checks allowed lineage before matching business row identity.
Ordinary requeue retains both original epochs, without adding the current epoch
to pass validation. The recovery manifest explicitly defines which lineages
may be reconciled; old payloads/event IDs are not relabeled.

Recovery snapshots use new approved event IDs and source lineage,
preserving the origin business identity. Consumers explicitly activate a replacement
projection rather than counting a recovery snapshot as a new
delivery. Consumer checkpoints use (source_epoch, delivery_id),
global event-ID dedup, and monotonic versions in the active lineage. Old/foreign epochs
must not revive deleted/revoked data. Consumer inventory
and schema rollout approval remain open before event v2 is enabled.

Each executor reserves a device only after concurrency/rate/breaker permits.
One unresolved claim cannot be taken by two executors in the same lease
generation. Results match the attempt ID and generation; a crash after the claim
commits preserves the charge, rather than speculatively refunding it. Lease takeover and unknown
external outcomes can still cause duplicate physical pushes: exactly-once
provider delivery is not promised.

## 5. Decisions outside the repository and bounded milestones

| Gate | Condition / owner still to be determined | Blocks |
| --- | --- | --- |
| G1 Scope | Core confirmed; unsupported inputs must be explicit | SPEC contract boundaries, not baseline tests |
| G2 Freshness/performance | TTL, numeric SLO, acceptance target, and aggregate quota not yet agreed | Final scheduling/retry configuration, benchmark acceptance, and release |
| G3 Mobile/auth | kaiapps2 path/branch found as first consumer; SDK/native behavior not yet read. Trusted issuer/audience/proof/revocation required per application, not global Laravel | Enrollment/receipt auth SPEC acceptance and cutover |
| G4 Infrastructure | Kafka hosts/failure domains, DB/staging/TLS, and operators unknown | Production deployment/HA claims |
| G5 Security policy | Multi-app requirement confirmed; ADR-12 app grant/control-plane details remain proposed. Business role matrix confirmed by user; M02a scoped SPEC and operation guards locally verified. Enrollment proof/recovery, gateway/BFF/operational policy remain open | Enabling modern enrollment/identity/perimeter and remaining security SPECs; does not block M02a role enforcement. C21 remediated within M02a scope; remaining security gates stay open |
| G6 Data/recovery | Retention, erasure, RPO/RTO, backup ownership, and consumer inventory undetermined | Maintenance/recovery SPEC acceptance and release |
| G7 Firebase/cutover | Project/app/APNs configuration, credential access, legacy automation/exclusive cohort unverified | Real provider/device pilot and OneSignal retirement |

Baseline test repair and staged processor review can start without
Firebase/Laravel server access or final SLO numbers. Sensitive implementation follows
a milestone SPEC recording the relevant gates; do not accept all ADRs
in bulk merely to start coding. Step 15 remains conditional until required
decisions are closed and ADR acceptance is recorded per document.

## 6. Required verification cases in subsequent SPECs

| Case | Evidence required, not yet executed |
| --- | --- |
| Concurrent send/revocation/receipt/registration | Bounded completion, no reverse locking or changes to another owner |
| Audience changes between count and insert | One committed snapshot/count; aborted transactions leave no partial intents |
| Two executors select the same device | One committed attempt ID/charge in that lease generation |
| Breaker opens midway through a page | Candidates without permits not yet charged; admitted in-flight work measured |
| Scheduler down past deadline | FAILED/ttl_expired, no fresh TTL or provider request |
| Expiry before fanout | Unknown denominators, no false COMPLETED/zero-device result |
| OPENED receipt on FAILED while PROCESSING | Late observation/read stored, campaign gains no false submission success |
| Topic deleted/recreated with same offset | Different source incarnation; old DLQ does not overwrite new |
| Restore and old Kafka numeric ID match | Origin/lineage mismatch held; no resend due to ID collision |
| Recovery snapshot after newer external projection | Explicit lineage activation/replacement; no double counting or resurrection of erased data |
| Campaign scheduled far in the future | Capability valid for send window, not expired from draft/schedule creation time |

Document checks cover local links, ADR structure, whitespace, and text
consistency. All concurrency/failure cases in this table require integration tests;
this review establishes no runtime, load, mobile, or restore results.

## Multi-application amendment (2026-11-03)

ADR-12 adds a normative scope requirement to the historical ADR-01–11 review;
old unqualified API/schema examples are not permission to omit app context.
No original reliability/epoch/lock-order invariant is removed.

| Existing ADR | Required application amendment, not implemented |
| --- | --- |
| 01–03 retry/worker/outbox | App-qualified intents, policy/holds/idempotency/events/recovery; validate app against canonical campaign before work |
| 04 provider | Registry-owned app/project/platform route and credentials; app-bound enrollment, conflict/proof and immutable binding |
| 05 receipts | Capability app/deployment context, app-bound owner/binding and scoped dedup/statistics |
| 06 cutover | Per-app/cohort migration and exclusive routing; kaiapps2 is one integration example |
| 07–08 Kafka/operations | Preserve current key bytes; scoped event verification, shared project quotas/app fairness, app-aware holds/observability |
| 09 API | Explicit application paths/current grants, all read/filter/count/write scope, no optional/default-app fallback |
| 10 recovery | Preserve app ownership/namespace, grants/route identity and per-app tombstone floors across approved restore |
| 11 security | Role AND current app grant, per-app issuer/origin proof and environment separation; new cross-app negative tests |

CR10 design acceptance is conditional; the old CR1–CR9 local reasoning and M02
single-app tests are not A/B isolation evidence. Existing Kafka key migration
and transaction lock-order review requirements remain in force.
