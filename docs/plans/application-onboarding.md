# Application onboarding delivery plan

Date: 2026-11-14. Status: **implementation in progress; setup/OAuth slices implemented locally**.
See the [checkpoint](../analysis/application-onboarding-implementation.md) for exact
implemented boundaries and remaining work. Staging has not been changed.
[Spec](../specs/application-onboarding.md) · [ADR-13](../adr/adr-13-application-onboarding.md) ·
[Detailed design](../design/application-onboarding.md) ·
[Proposed OpenAPI](../contracts/proposed/application-onboarding.openapi.json) ·
[Implementation tasks](application-onboarding-tasks.md).

## Review checkpoint

Enrollment-before-delivery and explicit SUPER_ADMIN release after testing are
user-confirmed (2026-11-14). Hosting is an office on-premises VPS. Read-only
inspection through `ssh kafka-vps` succeeded after VPN/SSH-agent setup. Observed:
separate Docker backend containers, non-root UID/GID 65532, read-only roots and
read-only policy volumes. No Compose projects were listed. Use this as the
starting point for the deployment adapter; verify the existing launcher and
rollback process before implementing it. Protected-file storage was subsequently selected by the user. Disk encryption,
access audit, backup/key custody and tested recovery still need evidence. No secrets were read or remote changes made.

The detailed design now defines the file provisioner/Docker adapter interfaces,
enrollment/approval policy, schema/authority and failure handling. Remaining live
inputs are named custodians, encryption/unlock, independent backup destination,
signed platform profiles and measured capacity. Implementation tasks are separate
from these operational release gates. The user subsequently authorized implementation; the checkpoint distinguishes
completed setup work from the remaining privileged/runtime/UI slices.

## A. Planning artifacts completed

- [x] Record the selected protected-file storage and accepted product decisions.
- [x] Specify restricted host provisioning and immutable Docker runtime generations.
- [x] Specify enrollment templates, greenfield provenance and scoped approvals.
- [x] Specify test-cohort, PILOT/FULL/HELD behavior and signed test receipt evidence.
- [x] Write proposed OpenAPI, body/response shapes, idempotency and errors.
- [x] Define logical schema, scoped constraints, DB capabilities and lock order.
- [x] Define leases, retention, key rotation, failure recovery and release gates.
- [x] Separate coding prerequisites from live production acceptance inputs.

These checkmarks mean documented, not implemented or verified in production.
The following sections describe future work; use the task inventory for execution.

## B. Candidate configuration and safe readiness

- [ ] Add paired migrations for scoped setup/configuration/operation/check metadata,
      constraints, indexes, audit allowlists and durable reconciliation work.
- [ ] Add SQL/repositories, then regenerate sqlc and affected mocks using pinned
      tooling. Keep immutable credential versions and setup revisions distinct.
- [ ] Add protected-file provisioner with per-app/environment/version isolation,
      root-owned 0640 files, trusted ancestry, atomic publication, provisional
      references and cleanup. API receives no root/Docker access.
- [ ] Ensure upload material never enters persistent jobs, logs or unprotected
      proxy buffers; jobs contain opaque references only.
- [ ] Add authenticated candidate upload, safe readiness reads, strong conditions
      and durable idempotency. Keep activation and sending disabled.
- [ ] Exercise rollback between secret creation and DB commit, duplicate requests,
      cross-app access, authorization revocation and accidental secret logging.

Likely locations: `internal/features/applications/`, `internal/database/repository/`,
`db/queries/`, `db/migrations/`, `internal/audit/`; a new narrowly scoped
protected provisioning package, named during contract review.

Exit evidence: app A/B can configure distinct candidates through real API/DB
boundaries, with redacted reads/audit and recoverable orphan handling.

## C. Bounded verification

- [ ] Reuse `fcm.NewFile` and `deployment.ReadSecret` for verifier/worker scoped
      read-only mounts, preserving current file and transport/token protections.
- [ ] Implement leased verification jobs, scope/version evidence, safe error codes,
      rate limits, freshness and provider failure classification.
- [ ] Test simulated OAuth rejection, timeout, cancellation, retry bounds, invalid
      keys, superseded work and app/project swaps with synthetic credentials.
- [ ] Add dry-run permission checking after scoped test enrollment is available;
      keep missing-device checks pending, not false failures or successes.

Exit evidence: PROVISIONING readiness accurately distinguishes local/OAuth
verification from pending target permission and delivery checks.

## D. Runtime preparation and enrollment activation

- [ ] Implement selected deployment reconciler with immutable per-app policies,
      resource limits and version/epoch-bound acknowledgements.
- [ ] Provision platform enrollment/receipt/address keys and source policies;
      verify Kafka, admission, scheduler and delivery composition.
- [ ] Add narrow DB-delegated greenfield activation and real role-denial tests;
      retain the existing offline path for imported projects.
- [ ] Add asynchronous activation API and crash/retry/supersession recovery.
- [ ] Demonstrate ACTIVE with dispatch HELD and zero devices, followed by normal
      enrollment; creator still needs explicit staff business grant.

Likely locations: new control-plane process/package, `internal/daemon/`,
`internal/deployment/`, `internal/installation/`, protected SQL profiles/functions
and corresponding daemon/lifecycle integration harnesses. Planned entry points
are `cmd/controlplane` and `cmd/provisioner`; keep deployment authority outside
server startup.

Exit evidence: no manual per-app SQL/mount/restart; seal, migration hold, stale
revisions/epochs and revoked actors prevent unauthorized completion.

## E. Guided pilot, full release and dashboard integration

- [ ] Expose reviewed route/cohort/consent/quota prerequisites and pending actions.
- [ ] Implement approved pilot/full delegation and APIs under separate capabilities.
- [ ] Integrate test notification as an existing scoped campaign under PILOT;
      never bypass acceptance, audience or physical-attempt authorization.
- [ ] Build dashboard steps: create → configure → activate enrollment → enroll/test
      → release. Show ACTIVE and delivery-held as distinct facts.
- [ ] Redact secret input across browser, BFF, tracing, proxies and error telemetry;
      discard input after submission, support replacement without revealing it.

Exit evidence: two-app simulated end-to-end flow proves isolated device enrollment,
permission verification, pilot test and full release, including a failed setup path.

## F. Rotation, recovery and rollout

- [ ] Add candidate promotion, dispatch hold/generation fences, worker drain and
      version switch; require fresh release after revalidation.
- [ ] Test uncertain provider I/O, incident hold during promotion, revoked staff,
      reconciler restart, protected-storage outage and restore into new runtime epochs.
- [ ] Define source/volume copy inventory, old-version retention/cleanup and
      Google IAM revocation responsibility.
- [ ] Demonstrate all six production gates in spec section 9.1, including active
      storage encryption, file-access audit and independent off-host restore.
- [ ] Extend local development with two synthetic apps and simulated OAuth/FCM.
- [ ] Update API/database/architecture/dashboard docs and lifecycle/dispatch/secret
      runbooks when implementation actually replaces the stated behavior.
- [ ] Roll out behind an explicit disabled-by-default deployment capability;
      pilot one provably greenfield app, then a second before wider enablement.

Rollback disables new onboarding execution and holds affected dispatch, fences
pending operations, preserves audit/attempt evidence and reconciles active worker
versions. Disabling only the HTTP route is insufficient. Do not drop retained
credential-operation history or automatically reenable older policy generations.

## Validation commands and limits

No Go tests are required for this documentation-only proposal. During implementation,
start with the affected packages, for example:

```sh
go test ./internal/features/applications/...
go test ./internal/fcm/...
go test ./internal/daemon/...
go test ./internal/installation/...
```

Use `GOCACHE=/tmp/hello-go-review-cache` if needed. For noisy diagnostics:
`python3 scripts/quiet-go-test.py -- ./internal/features/applications/...`.
Read `docs/agent-reference.md` Commands before integration work. Use the existing
`bash scripts/application-lifecycle.sh` harness as applicable and extend real DB
role tests for the new delegation. Container-dependent checks must report blocked
execution honestly; Go unit tests cannot establish DB-role or deployment safety.

Shared schema/authz/runtime changes also require `go test ./...`, `go build ./...`
and applicable `bash scripts/ci.sh` checks. Create paired migrations before
`make sqlc`; Make requires `.env`. Use `go tool mockery` for affected interfaces.
Document exact new harness invocations when implemented instead of inventing
commands for packages/processes that do not exist yet.

Live Google testing is an explicit separate exercise with authorized test-project
credentials and devices. Record OAuth, dry-run, submission and client receipt
separately. Simulations and green unit checks do not establish production readiness.
