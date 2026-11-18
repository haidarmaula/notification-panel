# Application onboarding implementation checkpoint

Date: 2026-11-18. Status: setup and OAuth verification implemented locally;
end-to-end onboarding and production enablement remain incomplete.

## Implemented boundary

- ONB-05 now supports immutable delivery policy publication on Linux and a
  protected `controlplane preflight-delivery` command. M46 pins the exact policy
  bytes, configuration and dedicated worker LOGIN to the live reservation. The
  host runs a bounded Docker check, verifies the worker's actual app/digest
  report and rechecks the reservation. Startup checks cover the worker database,
  registered source, Kafka metadata and local credential format without sending.
  The preflight adapter can take over the exact inert allocation without reserving
  a second slot; lost removal responses are resolved by inspecting the original
  deterministic name on retry. Live delivery authority and shared-process generation
  acknowledgements remain pending.

- Migration 000042 adds setup revisions, immutable app/environment credential
  versions, ingestion reservations, typed operations, checks, acknowledgements,
  approvals and replay snapshots. Existing apps are marked non-greenfield;
  new registry inserts get a setup row in the same transaction. No implicit grant.
- Migration 000043 adds app/DB-role scoped OAuth verification leases and guarded
  completion. Actor/session/generation, revisions and runtime epochs are fenced.
  Migration 000044 adds the final claim locking and exhausted-lease revision
  corrections while preserving the exact already-applied 000043 source.
  At most three expired claims are recovered before a terminal inconclusive result.
  Successful acknowledgement replay does not duplicate checks/audit or reopen holds.
- `cmd/provisioner` accepts only reserved credential staging over verified mTLS,
  from one pinned client URI identity. Root-owned Linux files are published with
  fixed permissions, no caller paths and read-only consumer mounts. Credentials
  never enter a DB/outbox payload; only safe metadata and keyed fingerprints do.
- `cmd/controlplane` runs one application's OAuth verifier, using a protected
  fixed policy path and the existing file credential boundary. Its separate
  `prepare-runtime` command uses fixed protected runtime and preparation policy
  paths plus a restricted per-app LOGIN to reserve/check capacity before a
  stopped Docker allocation. It does not start workers, acknowledge readiness,
  perform lifecycle transitions, approve routes or execute dispatch release.
- Eight optional setup routes are wired only when the protected server onboarding
  policy exists. [Implemented API](../api/application-onboarding-api.md). No new
  environment toggle or permission fallback enables them by accident.
- The BFF now forwards the same eight setup endpoints, with concurrency and replay
  headers, bounded retry delays and same-app operation locations. Query strings
  and duplicate concurrency headers are rejected; the larger upload limit applies
  only to FCM candidate staging. Local startup can explicitly select
  `--local-onboarding` with a fixed protected policy file; ordinary local and
  production defaults remain unchanged.
- Registry detail links to the dashboard setup screen. The screen displays the
  exact managed app, current state, profile form, file upload and OAuth outcome.
  Unknown writes retain their original idempotency key and ETag for explicit
  retry; a stale revision blocks writes until explicit review. Credential material
  is held only in component memory, never query/browser storage or a text preview.
- The synthetic local fixture has three additional fixed verifier slots for new
  staging apps. A trusted local-only allocator binds each role once and publishes
  a scoped policy, without a Docker socket or privileged HTTP command. It accepts
  missing dispatch policy only as an OAuth-only held app, without inventing TTLs
  or creating release policy. Production runtime allocation remains unimplemented.
- Protected mobile policy composition can append a new approved project while
  preserving existing projects and address keys. It rejects application replacement
  and shared audiences/namespaces, validates both generations with the server's
  parser, and does not restart a process or grant enrollment authority.
- A protected delivery runtime planning subset validates pinned image/network,
  bounded resources and capacity including replacement overlap. It produces
  scoped delivery plans and explicit shared-component acknowledgement requirements.
  [Subset contract](../design/application-onboarding-runtime.md).
- Migration 000045 adds durable per-app runtime capacity reservations under an
  owner-installed, immutable host capacity profile and per-app executor LOGIN
  scopes. Exact retries retain their original reservation UUID and deadline;
  conflicting retries, stale snapshots and capacity exhaustion fail closed.
  Reservation checks bind the live setup/configuration, generation, runtime
  epochs and profile digest. Expiry does not free an allocation whose external
  outcome is uncertain: release is eligible only after the original preparation
  deadline plus 30 seconds and requires an explicit scoped executor attestation
  that the runtime is absent. The trusted executor must first quiesce preparers
  and reconcile pending/uncertain Docker effects; elapsed time is not absence
  evidence. Immutable history is retained, and rollback refuses any retained
  profile, scope or reservation. The owner-only installer
  `deploy/postgres/onboarding-runtime-grants.sql` grants the restricted executor
  group only CONNECT/USAGE and the three definer capabilities; it does not create
  roles, capacity profiles or per-app scopes. This foundation
  grants no lifecycle, current-configuration, readiness-acknowledgement or
  dispatch-release authority.
- A typed Docker preparation adapter can create a deterministic, stopped worker
  allocation through a fixed protected Unix socket. It checks an exact-plan
  reservation before inspection and creation, refuses drift or a running
  allocation, and reconciles create conflicts and lost responses by inspection.
  The allocation has no network, mounts, environment or credential access, uses
  a fixed worker command and unprivileged UID, and enforces resource, read-only
  filesystem and capability restrictions. It cannot start/delete containers or
  acknowledge a runtime. The protected controlplane command uses the adapter;
  no public API exposes it. A stopped allocation is not a prepared functional
  notification runtime.
- GET readiness names `checks_configuration_id` (candidate first, then current) and
  returns explicit `NOT_CHECKED` entries for missing local-format, OAuth,
  target-permission and device-delivery evidence. OAuth success cannot implicitly
  pass the latter two checks or describe a different configuration version.
  Synchronous mutation/replay snapshots may retain empty checks; historical replay
  snapshots may have a null check configuration until their replay window expires.
  Clients refresh GET readiness after writes rather than infer evidence from them.

The ordinary staging deployment has not been changed. The implementation is not
an automatic replacement for the currently approved offline lifecycle procedure.

## Validation and practical limits

The subsequent M46 slice passed real PostgreSQL approval and role checks, Linux
policy publication tests, simulated Engine failure/replay tests and the existing
stopped-container Docker fixture. The actual worker preflight ran in Docker on
the synthetic local stack with a temporary restricted LOGIN. The reported app
and digest matched, delivery ledger counts stayed unchanged, and no accepted
OAuth or FCM requests were recorded. The rebuilt stack passed setup/OAuth through
the BFF and immediate/scheduled simulator delivery for its existing active app.
The full race suite passed (1,669 tests), along with build, vet, format, module
verification and the reachable vulnerability scan. The combined host CLI sequence
remains untested against one live fixture. See the
[runtime contract](../design/application-onboarding-runtime.md#protected-delivery-preflight).

The 2026-11-18 preparation/readiness slice passes focused package tests, the full
Go race suite (1,644 passed; opt-in Docker and Linux-root publication tests skip
in the ordinary Darwin run), CI provider/local-runner checks, format, module
verification, build and vet. `make sqlc` regenerated the new metadata models.
Both PostgreSQL fixtures and the explicit real Docker preparation fixture pass.
The rebuilt `./dev up` stack also passes `./dev check-onboarding` through the BFF;
the new app remains PROVISIONING/HELD. `./dev smoke` passes immediate and scheduled
simulator acceptance through the existing active application's real pipeline.
These distinguish existing delivery from incomplete greenfield onboarding.

During the earlier M45 slice, vulnerability scanning failed fetching
`vuln.go.dev` because DNS timed out. Full OpenAPI validation could not run: installation of the repository-pinned
Python validators failed resolving PyPI, including outside the sandbox. JSON
syntax, required-property references and local OpenAPI references were checked;
these are not substitutes for the full validator. Navigation/safe-example checks
pass. No new full-flow, device or production acceptance is claimed.

Focused Go tests cover strict JSON limits, malformed input, current DB authority,
scoped identifiers, safe errors, request/response redaction, submission snapshots,
readiness freshness, fixed-origin mutual TLS and verifier lease failures. The real
PostgreSQL fixture covers migration/rollback, cross-app FKs, immutable evidence,
least-privilege profiles, revocation, epochs, retry fences and bounded recovery.

Use `bash scripts/application-onboarding.sh` for the local network-isolated
PostgreSQL fixture. It never uses staging credentials. The fixture passed again
after the final verification-lock and exhausted-lease recovery changes.

Repository CI test (including race detection), build, vet, module verification and
format checks passed for the setup/OAuth implementation. The vulnerability scanner
reported no reachable vulnerabilities; it also reported dependency findings in
code not called by this module. Linux arm64 root-run file-publication tests passed,
including immutable publication and symlink rejection. The control-plane Docker
target built successfully. Full eight-image smoke coverage has not been run.

The additional mobile composition helper has focused tests for preservation,
cross-app identity rejection and malformed/oversized input. It is not yet wired
into a host policy publisher or restart protocol.

Docker adapter tests use a local simulated Engine HTTP endpoint. They exercise
the bounded stopped-allocation contract, drift rejection and create-response
recovery. `bash scripts/application-onboarding-docker.sh` also passes against a
real local Docker daemon with a cached pinned worker image: stopped create,
inspection, exact replay and scope-checked cleanup. That smoke uses a fake
reservation guard to isolate Engine behavior; the SQL guard is exercised by the
separate PostgreSQL fixture. Neither establishes combined DB/Docker orchestration,
runtime policy installation, shared acknowledgements, enrollment activation or
notification delivery. No container is started by the smoke.

`bash scripts/application-onboarding-runtime.sh` passes against isolated synthetic
PostgreSQL. It exercises scoped LOGIN/owner/unscoped denial, exact replay and
digest binding, concurrent global capacity across applications, replacement and
DB-connection budgets, expiry retaining uncertain allocations, explicit scoped
release, setup-revision and membership-revocation fences, rejection of accidental
direct UPDATE grants, no activation/current-pointer authority, and migration
rollback/reapply on an empty fixture, and refusal to roll back retained history.
Installer checks cover idempotence, nonowner denial, NOLOGIN group requirements
and absence of excess data/lifecycle authority. This is database enforcement
evidence, not an end-to-end new-app activation test.

The additional BFF/config changes pass focused race tests, including upload
limits, unsafe locations, queries, duplicate headers and protected local-policy
loading. Full repository race/orchestration checks also pass. `./dev up` preserves
the existing database and starts the real provisioner and scoped verifier with
synthetic identities. `./dev check-onboarding` passes through the real BFF:
profile selection, immutable upload/replay, queued OAuth verification and polling,
plus cross-app operation denial. The demo remains PROVISIONING with dispatch HELD.
The real HTTP check also covers empty installation-array persistence for VERIFY;
nil pgx slices previously produced SQL NULL and are now normalized at that boundary.

The local fixture receives only its password seed input and generated-policy
output directory, rather than the onboarding policy/key directory. Reused demo
state must still be provisioning and held. Synthetic service-account/public-key
material is preserved across restarts, while transport certificates can refresh.
`./dev smoke` also passes immediate and scheduled simulator delivery through the
existing pipeline, and `./dev test-seed` passes transactional failure/retry in its
separate temporary database. No staging deployment was changed.

The dashboard setup screen passes a real local browser flow for a staff-created
app, including automatic assignment of a separate verifier, profile editing,
candidate upload and successful OAuth. Credentials are absent from rendered text
and browser storage. Desktop, light mobile and dark mobile captures were reviewed.
Focused browser regressions cover explicit stale-version review, same-key/ETag
retry after a lost upload response, and 320px layout with maximum-length names.
The combined registry/staff/onboarding browser run passed 13 cases, dashboard unit
tests passed 11 cases, and the local orchestration suite passed 15 cases. Build,
lint and the full Go race suite pass. No device delivery or activation is implied.

Native Darwin tests cannot establish Linux root ownership/ACL publication. Live
Google authentication, target IAM permission, device observation and production
backup/restore remain distinct acceptance evidence. OAuth success is not readiness
to deliver, and no new general-send endpoint or automatic ACTIVE transition exists.

## Remaining implementation

1. ONB-05/06: extend the validated delivery policy to a runnable, generation-fenced
   worker with app-scoped execution authority. Complete shared policy publication,
   serialized rollout and authenticated live acknowledgements, then install the
   DB-delegated first-activation executor with SQL-role and crash recovery tests.
2. ONB-07/08: compose immutable mobile trust/profile generations, approved external
   identity mapping, test cohort/route approval and target-permission dry-run.
3. ONB-09/10: guarded pilot/full release, signed per-platform test receipts,
   credential promotion/drain/rollback and reference-checked orphan/old-file cleanup.
4. ONB-11/12: activation/device/release dashboard steps, production runtime and verifier allocation,
   independent encrypted backup delivery, operational metrics and rollout drills.

The production verifier scope registry is installed by the trusted DB owner for a
restricted per-app verifier LOGIN. The local synthetic allocator does not provide
production per-app provisioning.
Reconcile those role OIDs against the target cluster after restore before starting
verifiers. Fingerprint key changes also require preserving the replay window;
a multi-key replay migration/rotation path is not implemented in this slice.

Do not mount future activation/promotion/release handlers merely because request
DTOs and intent-validation code exist. Their executors and privileged transitions
are not implemented or validated, and remain absent from the implemented OpenAPI.

The [frontend handoff](../api/application-onboarding-frontend-handoff.md) records
the eight active endpoints, request examples, error/polling/replay handling and
the actions that must remain unavailable until these backend gates are complete.
