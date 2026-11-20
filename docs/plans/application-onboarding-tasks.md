# Application onboarding implementation tasks

Date: 2026-11-18. Status: implementation authorized and in progress. Setup and
OAuth slices have local code/tests; the full onboarding flow is not complete.
[Current checkpoint](../analysis/application-onboarding-implementation.md).
[Design](../design/application-onboarding.md) · [Spec](../specs/application-onboarding.md) ·
[Sequence](application-onboarding.md) · [Proposed API](../contracts/proposed/application-onboarding.openapi.json).

## Current progress

| Slice | Status |
| --- | --- |
| ONB-01 | Metadata schema/query/audit foundation implemented; real PostgreSQL fixture passes |
| ONB-02 | Root-owned file publication and reserved mTLS ingestion implemented; automatic orphan cleanup remains |
| ONB-03 | Eight optional setup routes, safe responses, BFF forwarding and dashboard setup forms implemented; activation/device/release steps remain |
| ONB-04 | Scoped OAuth worker and fenced completion implemented and exercised through the real local BFF/provisioner; target IAM/dry-run is part of ONB-08 |
| ONB-05 | Partial: protected delivery plan/mobile composition, scoped durable reservations (M45), immutable worker policy publication and owner-approved one-shot delivery preflight (M46), including takeover of the exact inert allocation; app-scoped live delivery and shared runtime rollout with authenticated generation acknowledgements remain |
| ONB-06 | Activation API, app-scoped issuance and separate execution verified from a new registry application to ACTIVE/HELD in the services fixture |
| ONB-07 | Protected mobile policy and HTTPS enrollment verified with synthetic external identity and one Android test device |
| ONB-08 | Scoped installation list, protected roster approval, HTTPS cohort operation, provider dry-run and per-target polling verified with the synthetic provider |
| ONB-09 | Protected route, PILOT and FULL workflows verified with HTTPS campaign/send, simulator acceptance, signed DELIVERED, grant recovery and replay |
| ONB-10 | Pending: held credential promotion, drain/rollback, recovery and reference-checked cleanup; promotion handler is not mounted |
| ONB-11 | Partial: existing setup dashboard uses eight active endpoints; activation/device/release UI remains for a separate frontend session after backend gates; [frontend handoff](../api/application-onboarding-frontend-handoff.md) records current contract |
| ONB-12 | Pending: production allocation, operational metrics, encrypted off-host backup/restore and staged rollout evidence; local simulator success is not production enablement |

## Ordered slices

| ID | Deliverable and likely files | Depends on | Acceptance evidence |
| --- | --- | --- | --- |
| ONB-01 | Final API/schema implementation mapping; new paired migrations in db/migrations and queries in db/queries; audit constants | Approved planning | Migration cycle, composite app FKs, revision/idempotency uniqueness, preservation of security history |
| ONB-02 | Protected ingestion service and client, app/version files, provisional-reference protocol; new internal/provisioning package and host service | ONB-01 | Synthetic upload, strict parser, ownership/mode/ACL/symlink/path rejection, fsync/atomic publication, no raw secrets in durable state |
| ONB-03 | Setup/profile/readiness/candidate API, BFF redaction configuration and audit | ONB-02 | Proposed API request/response checks, 401/403/404, 412/428, concurrent replay, stage/DB rollback, two-app isolation |
| ONB-04 | Leased control-plane process, bounded FCM verifier using existing file source | ONB-03 | Simulated OAuth failures/timeouts, freshness, canceled/superseded workers, no permission claim without scoped dry-run |
| ONB-05 | Protected platform profile and Docker preparation adapter; scope Kafka/admission/scheduler/delivery/server policies and resource reservations | ONB-04 | Dry preparation of two apps, fixed image/mount allowlists, immutable policies, overlap capacity and other-app preservation |
| ONB-06 | Narrow delegated lifecycle/DB guard installation and async first activation | ONB-05 | Real role-denial, revoke/revision/epoch/hold races, exact retries, atomic pointer+ACTIVE/HELD, zero-device enrollment |
| ONB-07 | Enrollment profile integration and external broker fixture, existing mobile registration flow | ONB-06 | Distinct audience/namespace per app, correct origin/subject map, no app spoofing or inherited business grant |
| ONB-08 | Scoped evidence import/list, delegated M37 test-cohort approval and target-permission checks | ONB-07 | Signed/revoked/expired/foreign evidence rejection, binding epoch changes, cohort bounds, dry-run never sends |
| ONB-09 | Delegated PILOT/FULL and synchronous incident hold, current-campaign test evidence | ONB-08 | Existing campaign grants/send checks, PILOT no broadcast, per-platform valid receipt before FULL, late holds defeat pending completion |
| ONB-10 | Credential promotion/drain/recovery and retention cleanup | ONB-09 | Replacement and rollback remain held; old process cannot start; uncertain attempts preserved; revocation and cleanup reference checks |
| ONB-11 | Dashboard wizard consuming the approved API; local two-app simulated fixtures | ONB-09 (rotation UX after ONB-10) | Create/configure/activate/enroll/test/release flow, actionable blockers and secret-redaction verification |
| ONB-12 | Operations docs, audit/metrics, encrypted off-host backups, restore/rotation rehearsal and staged release | ONB-10, ONB-11 | Six production gates satisfied, measured RPO/RTO/capacity, imported staging app adopted explicitly, reviewed enablement |

Keep each slice small; split its persistence/API/runtime work into reviewed commits
when necessary. Use the current checkout and scoped Conventional Commits on main,
following repository instructions. Planning authoring does not request a commit
or deployment. Do not implement SQL authority and host privilege changes through
an unreviewed generic CRUD abstraction.

## Failure matrix required across slices

| Boundary | Failure to exercise | Required outcome |
| --- | --- | --- |
| Upload | Client/provisioner timeout, lost acknowledgement, concurrent replay | One candidate or explicit retryable failure; no leaked or current orphan |
| Database | Audit insert failure, stale ETag, transaction deadlock | No partial mutation; retry only DB work or idempotent named stage |
| Staff | Session revoked, role changed, staff deleted during job | Pending management operation cannot complete |
| Scope | Swap app/config/cohort/device/evidence/project IDs | Uniform denial and no cross-app secret access |
| Runtime | Seal, migration hold, changed epochs, stale worker acknowledgement | No activation/release across the fence |
| Device | Reassigned owner/binding/route after verify/test | Invalidate relevant evidence; no release using prior identity |
| Provider | OAuth rejection, transient outage, ambiguous send | Accurate safe classification; no credential-triggered blind resend |
| Docker | Crash after stop/create/start; overlapping generations | Journaled recovery, fenced old starts, no automatic FULL |
| Storage | Encrypted mount unavailable, permission drift, symlink swap, disk full | Fail closed; existing approved current remains usable where safe |
| Recovery | Host loss, backup corruption, missing key, restored epochs | Demonstrated off-host restore; explicit hold and lineage reconciliation |

## Verification and release checklist

Run package-local Go tests for each slice; SQL/privilege changes require the real
PostgreSQL migration and role harness. Do not treat mocks as privilege evidence.
Regenerate sqlc only after paired migrations and regenerate affected mocks with
`go tool mockery`. Shared authz/runtime/schema changes also need `go test ./...`,
`go build ./...` and relevant repository CI checks. Preserve full failure logs.

Planned control-plane observability: counts and age of queued/stale/failed
operations, unreferenced stage age, acknowledgement/capacity freshness, failed
verification by safe code, generation/hold transitions and off-host backup age.
Use bounded component/kind/outcome labels; do not use app UUIDs as unbounded metric
labels or include private material in logs. App-scoped audit provides correlation.

Live release blockers (operator supplies values):

- [ ] Named operator and separate recovery/key custodians.
- [ ] Verified active-storage encryption/unlock and file-access auditing.
- [ ] Signed platform, enrollment and evidence profiles; exact trusted identities.
- [ ] Independent backup destination, automatic copy, freshness alert and restore.
- [ ] Allocated aggregate provider, process and DB budgets, including replacement.
- [ ] Authorized test-project/devices for every enabled platform and valid receipts.
- [ ] Reviewed feature enablement, one-app pilot then second-app isolation evidence.

Leaving these unchecked does not prevent synthetic implementation work. It does
prevent calling the system production-ready or enabling it against real apps.
