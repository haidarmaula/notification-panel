# Application onboarding and FCM configuration

Spec origin: 2026-11-14. Status updated 2026-11-23: **setup, OAuth, activation,
cohort dry-run, release requests and the local dashboard flow are implemented;
production enablement remains gated**.
[Implementation checkpoint](../analysis/application-onboarding-implementation.md).
User-approved decisions and engineering defaults are distinguished in section 10.
Module id: `application-onboarding`.

This is the entry point for the proposed full onboarding target. The current
implementation covers a subset; the checkpoint describes implemented behavior,
while the production gates below remain acceptance requirements. Companion documents:
[credential/control-plane ADR](../adr/adr-13-application-onboarding.md) and
[delivery plan](../plans/application-onboarding.md),
[implementation design](../design/application-onboarding.md) and
[proposed OpenAPI](../contracts/proposed/application-onboarding.openapi.json). All endpoints, data models,
defaults and time limits below are proposed. Existing HTTP contracts and offline
procedures remain authoritative until explicitly replaced by implementation.

## 1. Objective and scope

A SUPER_ADMIN can create an application, supply FCM credentials, see actionable
setup results, activate enrollment and progress toward notification delivery
without manually editing SQL, mounting a secret or restarting a worker for each
ordinary new app. Platform installation still requires an operator to provision
trusted infrastructure, secret storage, deployment authority and capacity.

Success means two independently configured applications can complete this flow
and deliver through their own Firebase projects without sharing authority,
recipient identities or secret material.

Initial implementation scope (engineering defaults unless user-confirmed below):

- FCM HTTP v1 is the only provider. Upload a service-account JSON object; legacy
  server keys and mobile `google-services.json` are not accepted.
- Only current DB-authorized SUPER_ADMIN staff manage onboarding, matching the
  registry's management policy. Creating an app still grants nobody business access.
- Initial support is same-project service accounts. Cross-project IAM and keyless
  credential enrollment are deferred; existing reviewed deployments are preserved.
- One app/environment has one current FCM configuration. Choose the environment
  at initial setup and keep it immutable; staging and production use separate apps.
- Platform-specific app identifiers and receipt/enrollment requirements are
  explicit setup inputs, not inferred from a service-account project ID.
- UI implementation is later work; this spec defines its backend requirements.

Exclusions: cancellation, new notification targeting semantics, automatic legacy
migration, automatic consent, arbitrary provider URLs, APNs key upload, and
replacing mobile authentication or the protected attempt/recovery protocol.

## 2. Capability boundaries

These are components of one end-to-end capability, not independently approved work.

| Component | Owns | Consumes |
| --- | --- | --- |
| Registry | App identity, name, staff grants | Existing staff authority |
| Provider configuration | Candidate versions and safe setup metadata | Registry, protected-file provisioner |
| Verification | Bounded OAuth/FCM evidence for an exact candidate | Candidate and scoped test target |
| Onboarding reconciler | Runtime preparation and restricted lifecycle commands | Versioned desired state, platform authority |
| Readiness | Blockers and next actions | Current app, config, runtime and dispatch evidence |
| Dispatch release | Pilot/full approval using protected rules | Enrollment, route/cohort and quota evidence |

Dependency order: registry → candidate → verification → runtime preparation →
enrollment activation → enrollment/route review → pilot/test → full release.
Credential verification may finish partially before enrollment; a missing device
must never prevent enrollment activation.

## 3. Current behavior and proposed compatibility

Current creation is `applications.Service.Create`; registry routes manage only
metadata/grants. See [registry API](../api/applications-api.md).
`internal/daemon/delivery_config.go` pins one application and immutable delivery
policy, including credentials, routes, budgets and receipt/encryption material.
[File credentials](m06-fcm-credentials.md) accept only protected deployment files.
[Lifecycle](../runbooks/application-lifecycle.md) and
[dispatch](../runbooks/application-dispatch.md) use separate protected DB authority.

This proposal adds a control plane and an explicitly delegated execution path;
it does not grant the server direct writes to protected status/dispatch columns.
The delegation must be enforced in PostgreSQL and deployment policy, not just Go.
Existing imported apps continue using reviewed offline operations until explicitly
adopted. Imported ownership and legacy-exclusion evidence cannot be generated by
clicking an onboarding button. Normal app metadata DTOs keep their existing shape.

## 4. State and readiness

Keep application states `PROVISIONING`, `ACTIVE`, `HELD`, `RETIRED`.
`ACTIVE` continues to permit enrollment and authorized business operations; it
never promises unrestricted delivery. Keep dispatch `HELD`, `PILOT`, `FULL`.
Do not add FCM error values to the application status column.

| Step | App status | Dispatch | Preconditions/result |
| --- | --- | --- | --- |
| Create | PROVISIONING | HELD | Identity only; no secret, implicit grant or send |
| Configure | PROVISIONING | HELD | Immutable candidate stored securely |
| Verify/prepare | PROVISIONING | HELD | Valid credential evidence and runtime preparation |
| Activate enrollment | ACTIVE | HELD | Current configuration and platform checks pass; no device required |
| Enroll and review | ACTIVE | HELD | External client enrollment plus scoped route/cohort approval |
| Pilot/test | ACTIVE | PILOT | Explicit reviewed test cohort and bounded quota |
| Full release | ACTIVE | FULL | Fresh full-coverage approval after pilot |

Provider configuration has separate candidate and current version pointers.
Candidate states: `STORED`, `VERIFYING`, `VERIFIED`, `REJECTED`, `SUPERSEDED`.
A verified candidate is not current until a fenced installation succeeds. First
activation installs the initial current version; later changes use promotion. Rejection
of a replacement leaves the working current version intact.
Verification records separate `local_format`, `oauth`, `target_permission` and
`device_delivery` results: `NOT_CHECKED`, `PASSED`, `FAILED`, or `INCONCLUSIVE`.
OAuth success alone is insufficient to claim target permission. Enrollment can
activate with target permission and device delivery pending; dispatch stays held.

Readiness returns app status, dispatch mode, setup revision, current/candidate
configuration IDs, operation summary, checks, blockers and next actions. Use
allowlisted codes, for example `FCM_NOT_CONFIGURED`, `FCM_CREDENTIAL_REJECTED`,
`FCM_CHECK_INCONCLUSIVE`, `RUNTIME_PENDING`, `PLATFORM_HOLD`,
`ENROLLMENT_NOT_CONFIGURED`, `TEST_DEVICE_REQUIRED`, `ROUTE_REVIEW_REQUIRED`,
`DISPATCH_APPROVAL_REQUIRED`, `READY_FOR_PILOT`, `READY_FOR_FULL`.
Each check includes its scope and checked/expiry timestamps. Runtime health is
reported separately from historical verification; a readiness response is not
continuous proof that IAM or network access still works.

## 5. Proposed management HTTP contract

Prefix `/api/v1/admin/apps/{app_id}`; public UUIDs only. Existing API-key/staff
session authentication, current DB role and service/transaction guards apply.
All responses use the existing envelope and `Cache-Control: no-store`.
The future BFF must redact request bodies and never persist uploaded keys.

| Method and suffix | Input | Result |
| --- | --- | --- |
| GET `/onboarding` | — | 200 readiness and strong setup ETag |
| PUT `/onboarding/fcm` | `environment`, `target_project_id`, `service_account` JSON object | 201 candidate metadata and advanced setup ETag |
| POST `/onboarding/fcm/verify` | `configuration_id`, optional `test_installation_id` | 202 operation + Location |
| POST `/onboarding/activate` | `configuration_id` | 202 enrollment activation operation + Location |
| POST `/onboarding/fcm/promote` | `configuration_id` | 202 replacement promotion operation + Location |
| GET `/onboarding/operations/{operation_id}` | — | 200 scoped, sanitized operation outcome |
| GET `/onboarding/enrollment-profiles` | — | 200 approved safe template list |
| PUT `/onboarding/enrollment` | `profile_id`, platform/identifier entries | 200 readiness + ETag |
| PUT `/onboarding/test-cohort` | `installation_ids`, `reason`, `evidence_id` | 202 scoped cohort operation |
| GET `/onboarding/approvals` | — | 200 safe scoped approval bundle list |
| POST `/onboarding/dispatch/release` | `mode`, `cohort_id`, `evidence_id` | 202 PILOT/FULL operation |
| POST `/onboarding/dispatch/hold` | `reason` | 200 readiness; synchronous one-way hold |

FCM PUT is setup/replacement, never activation. GET never exposes the JSON,
private key, access token, secret locator, or deployment credentials. Safe metadata
includes configuration UUID, target project, service-account email, timestamps
and check results; these management values are not ordinary membership DTO fields.
Identifiers for operations/configurations from another app return 404.

All writes require the strong setup `If-Match` obtained from readiness. New apps
already have an initial setup revision. Missing condition: 428; stale: 412;
malformed: 400. Metadata ETags and setup ETags are different resources. Writes
also require `Idempotency-Key` (UUID). Same actor/app/route/key and identical
payload returns the original result for 24 hours; a different payload returns
409. An identical retry is recognized before evaluating its now-stale ETag,
but only after current authorization. Persist a keyed request fingerprint, never
the raw credential payload. First-time precondition checking and mutation are
atomic. Retention/cleanup must preserve pending operation deduplication.

Use existing strict JSON rules: exact keys, no duplicate/unknown fields, valid
UTF-8, one object, no trailing data, application/json, no content encoding.
FCM PUT total limit: 96 KiB; service-account object serialized limit: 64 KiB.
Other writes use the registry 64 KiB bound. Parse credentials with the existing
field/algorithm/identity restrictions, adapted for an authenticated upload source.
Pin Google endpoints; never follow credential URLs or accept a client file path.

Use 400 for malformed inputs, 401/403 for authentication/authority, 404 for absent
resources, 409 for invalid state or idempotency conflict, 413/415 for body/media
errors, 429 for rate limits and 503 for unavailable control-plane dependencies.
Unknown errors remain literal `internal server error`. Verification rejection is
a sanitized operation result, not a raw Google response. No status PATCH is added.

Operation states: `PENDING`, `RUNNING`, `SUCCEEDED`, `FAILED`, `SUPERSEDED`.
Kinds are VERIFY, ACTIVATE, PROMOTE, TEST_COHORT, RELEASE_PILOT and RELEASE_FULL.
Return operation ID, kind, timestamps, safe failure code and resulting revision.
Candidate evidence may advance without invalidating its own originating operation;
any newer desired configuration or lifecycle/epoch change supersedes stale work.
A successful operation never overrides a later hold. Provider timeouts are
inconclusive/retryable with bounded backoff, not proof that a key is invalid.

Full payload schemas, trusted enrollment templates, test-cohort/evidence handling
and release conditions are defined in the proposed OpenAPI and implementation
design section 3. Before FULL, each enabled platform needs recorded provider
acceptance and a valid DELIVERED/OPENED test receipt under the current config and
pilot generation, within one hour. PILOT permits the first bounded test and does
not require a prior successful real test. Existing offline approval remains
available for imported apps; it is not an undocumented API fallback.

## 6. Credential verification and device testing

1. Validate locally without external I/O or logging key material.
2. Obtain a bounded OAuth token using the fixed Google token endpoint and only
   Firebase messaging scope. Tokens remain in memory and expire normally.
3. Once an authorized app-scoped device exists, verify target permission through
   FCM HTTP v1 `validate_only` using that device. The verify request may supply
   `test_installation_id`, a public app-scoped installation UUID; never a raw token.
   Resolve its current binding and protected address server-side, and require a
   live installation, active owner, consent and approved test-device designation.
   Check app/environment/project and current binding epoch/route version. Bind
   permission evidence to those values and invalidate it when they change. A
   scoped reference from another app returns 404. With no installation reference,
   run local/OAuth checks only and leave permission pending; do not pick an
   arbitrary recipient. Do not use arbitrary/fake device tokens as conclusive
   evidence. Dry-run does not prove device receipt.
4. An explicit test campaign through the existing dispatch pipeline supplies
   actual submission evidence. Provider acceptance and app receipt are different
   results; neither proves that a user saw a notification.

No real send is triggered by upload, verification, activation, startup or health
checks. Test campaigns require an ACTIVE app, current grant/send permission,
approved pilot cohort and attempt budgets. No direct FCM-send admin endpoint.

Proposed limits: local verification job deadline 60 seconds, no more than three
network attempts with bounded backoff inside that deadline, one in-flight verify
per candidate, at most five new verify jobs per app per minute. Authenticated
upload limits also apply per actor. Successful OAuth evidence is reusable for
activation for at most 15 minutes and is bound to exact credential version,
project, environment and runtime epochs. Expiry requests a fresh check; it does
not silently deactivate an already active app. Dispatch must recheck its required
evidence at release. Provider/network failures remain possible after release.

## 7. Persistence and operation safety

Logical entities (planned table/constraint detail is in design section 4;
migration numbers are allocated during implementation):

- `application_onboarding`: app/environment identity, setup revision, desired and
  current config versions, enrollment profile reference, runtime generation.
- `application_provider_configurations`: immutable version, safe provider identity,
  internal secret reference, lifecycle state, creator and timestamps.
- `application_onboarding_operations`: actor/app/kind, deduplication fingerprint,
  desired version/revision/epochs, lease/attempt evidence and safe result.
- `application_provider_checks`: exact version/scope, check outcomes and expiry.
- Runtime acknowledgements: app, environment, config and policy generation,
  epochs, identity of authorized reconciler/worker, bounded lease expiry.

No raw key/token columns, secret references in public DTOs, or general-purpose
client-supplied policy JSON. Foreign keys and queries enforce application scope.
New actions/entities must be registered in `internal/audit/constants.go` and its
DB constraints. Audit only allowlisted app/config/operation IDs, outcome codes,
actor and revisions. Business mutation, audit and durable work enqueue commit
atomically. Protected filesystem storage cannot share this transaction: create a provisional
secret, commit its reference, then reconcile/garbage-collect unreferenced secrets
with a grace period and live-reference checks. Failed DB commits cannot produce
a usable current credential or an unrecoverable orphan.

The reconciler owns no arbitrary SQL/deployment command from a request. It issues
narrow commands bound to app, actor authority, operation, epochs and expected
revisions. Completion rechecks current authority and fences. Enforce one applicable
configuration generation, renewable bounded operation leases, crash recovery and
idempotent desired-state reconciliation. External effects must not occur while
holding a long-running DB transaction. Authority revocation prevents later stages;
already completed secret writes are reconciled, not hidden.

## 8. Activation, dispatch and rotation

Enrollment activation checks current staff authority, PROVISIONING state, fresh
OAuth verification, prepared runtime/enrollment policy and applicable platform
ownership/seal/migration gates. It creates no staff grant, route approval, consent
or dispatch release. Greenfield classification requires trusted creation lineage
and an approved no-legacy platform policy; an HTTP `greenfield=true` is not evidence.
First activation also requires no current configuration. Prepare the candidate
runtime in a non-sending state, then atomically install its current-version pointer,
advance setup revision and change PROVISIONING to ACTIVE through the delegated
transaction. That transaction checks the matching prepared acknowledgement; its
success never releases dispatch. On failure, any prepared runtime remains fenced
and is reconciled or removed. A retry returns the original operation outcome.
`/fcm/promote` is only for replacing an existing current version; it cannot race
first activation to bootstrap a different one. HELD recovery and RETIRED apps
are outside first-activation eligibility.

Worker preparation covers receipt/address keys, platform route allowlists,
provider/project binding, admission/scheduler source identity, Kafka authorization,
quotas and capacity. Credential upload alone cannot acknowledge readiness. Retain
one-app worker isolation initially. A platform reconciler provisions immutable
policy/process generations; do not mutate the already-loaded binary configuration
or enable ambient credential discovery. A new app may wait for capacity with an
explicit blocker rather than being reported fully ready.

Before any send, existing route/cohort/ownership, runtime seal, dispatch generation,
attempt lease, idempotency and ambiguity rules still apply. Greenfield enrollment
route authorization and consent use the approved scoped evidence policy in the
implementation design;
all FULL campaigns retain coverage checks. Migrated apps retain reviewed legacy
exclusion and separate approval requirements.

Rotation stores/verifies a candidate while the current credential continues serving.
Promotion places new dispatch on hold and advances its generation, drains/fences
old physical starts, reconciles in-flight evidence, prepares the new immutable
worker generation and atomically switches the current pointer using fences.
Invalidate old-version permission/readiness evidence. Revalidation and a fresh
release are required; do not automatically restore FULL. Retain the old secret
only for bounded recovery; rollback requires current checks and authorization.
Revoking a leaked Google key requires action in Google IAM as well as local hold.
Never replay an uncertain FCM send because a credential or worker changed.

## 9. Acceptance and validation

- A fresh app can reach ACTIVE/HELD with zero devices; next action is enrollment.
- Missing/invalid credentials block activation with safe actionable reasons.
- OAuth success alone never marks permission or delivery checks passed.
- Two apps use different secrets/projects, operations, workers and recipients;
  swapping any of those identities fails closed, including concurrent requests.
- Stale checks, stale revisions, changed epochs, revoked authority and incident
  holds defeat activation/promotion completion. Concurrent retries converge.
- Provisioner/storage timeout, DB rollback, reconciliation crash and unavailable capacity
  recover without leaked keys, duplicate active generations or false success.
- Key rotation and rollback preserve charged/uncertain provider attempt evidence.
- API/BFF/audit/log/error fixtures contain no secrets, including rejected JSON.
- Real PostgreSQL role tests deny server direct status/dispatch writes and reject
  forged/cross-app delegated commands. Existing offline-grant guarantees survive.
- A simulated end-to-end two-app flow includes enrollment, pilot, scoped test,
  full release and rotation. An explicit real test-project/device exercise is
  separate evidence, never a unit-test or simulated-delivery claim.

Go implementation follows the pinned module version in `go.mod` (currently
1.26.11), `docs/go-standards.md`, nearest feature conventions and package-local
`*_test.go` tests. Source locations and focused checks are in the companion plan.
Schema changes require paired migrations before sqlc generation; never edit
`internal/database/sqlc/` by hand. Preserve immutable config and current DB authz.

## 9.1. Selected credential storage and production gates

The user accepted reuse of the existing protected-file pattern with the following
extensions. [ADR-13 section 2](../adr/adr-13-application-onboarding.md#2-extend-the-existing-protected-file-credential-source)
is the storage boundary. PostgreSQL contains metadata/references only; no mandatory
OpenBao service or encrypted credential blobs are introduced.

A restricted host-side provisioner owns app/environment/version paths, atomic
root-owned file publication, safe provisional references and cleanup. API upload
is bounded and validated; the API gets neither privileged filesystem access nor
the Docker socket. Never persist upload bodies in DB operations, queues, request
logs or proxy temporary storage outside the protected ingestion boundary. Workers
and verifiers reuse the existing file parser and receive scoped read-only mounts.
Use immutable versions and fenced worker replacement for promotion/rotation.

Production acceptance requires evidence for all six areas:

1. **Isolation:** two apps cannot access each other's files/volumes, secret refs,
   verifier operations or worker configurations; shared UID/GID is insufficient.
2. **Storage:** active credential storage has verified encryption at rest,
   controlled unlock-key custody, restricted administrators and file-access audit.
3. **Provisioning:** authenticated scoped commands, bounded upload, no arbitrary
   paths/commands, race-safe atomic files and recoverable file/DB commit failures.
4. **Rotation:** replacement, Google IAM revocation and worker drain/recovery are
   tested; stale generations fail and uncertain attempts are never replayed.
5. **Recovery:** encrypted backups reach independent off-host storage; decryption
   keys have separate custody; a restore drill proves recoverability and fences
   restored runtime/credential generations. Document all source/volume copies.
6. **End-to-end failures:** two-app setup, failed provisioning, restarts, outage,
   concurrent rotation and incident holds pass focused and integration tests.

These are release gates, not assertions that staging already satisfies them.

## 10. Decisions and planning completion

User-confirmed on 2026-11-14:

- ACTIVE enables enrollment while general dispatch remains held. Explicit test
  delivery precedes opening general delivery.
- Only SUPER_ADMIN may approve opening delivery, explicitly after a successful
  test. Existing scoped business grants and send permissions still apply.
- Credential storage reuses protected per-app files/read-only mounts, with the
  additions and six production acceptance areas in section 9.1.
- Backend hosting is an office on-premises VPS. Staging is accessed through the
  user-provided `ssh kafka-vps` alias. This authorizes inspection, not deployment
  changes or implementation.

Read-only SSH inspection succeeded on 2026-11-14 after office VPN activation
and loading the staging identity into the SSH agent. Staging runs Docker with
separate server, admission, delivery and scheduler containers, plus dashboard,
gateway, PostgreSQL and Kafka. The four backend containers run as UID/GID 65532,
with read-only root filesystems, nonprivileged mode and distinct read-only named
policy volumes mounted at `/etc/hello`. No Compose projects were listed; the
actual deployment launcher and restart/restore process remain unverified.

Guest-visible storage is ext4 on LVM; no guest-visible dm-crypt layer was listed.
This does not establish whether the hypervisor/storage encrypts the underlying
disks. No Vault executable or container was observed in the limited inventory;
this does not rule out an external secret service. No secret contents, environment
values or credential files were read. No server changes were made.

Plan runtime provisioning around the observed Docker/read-only-policy pattern;
keep deployment authority outside the HTTP server and do not expose the Docker
socket to it. Follow-up metadata inspection confirmed root ownership, group 65532
and mode 0640 on both source and delivery-volume FCM files. Code review found
protected provisioning and encrypted credential/config backup implementation;
this is not a new live backup/restore test. The host-side provisioner and Docker
adapter details, disk encryption, access audit and recovery evidence remain open.
The user subsequently authorized implementation. Live enablement remains separate.

Planning is complete and implementation has started. The linked checkpoint is the
authoritative implemented/planned boundary; this product spec describes the full target. The implementation design selects restricted host provisioning and Docker
reconciliation, defines enrollment templates and scoped evidence/approval flow,
and records schema constraints, DB capabilities, lock order, leases, retention
and recovery defaults. [Tasks](../plans/application-onboarding-tasks.md) map these
to verifiable implementation slices.

Engineering defaults: same-project service accounts, separate staging/production
apps, FCM registration-token routes initially, no implicit business grants,
15-minute credential/permission freshness, one-hour test evidence, seven-day old
credential recovery retention and 90-day minimum operation metadata. These values
are explicit and reviewable, not claims of user approval or provider requirements.

Deployment inputs still required before live enablement: named operator/key and
backup custodians, verified encryption/unlock mechanism, independent backup
destination, approved platform/enrollment/evidence profiles and measured capacity.
The absence of those live values must be a visible blocker. It does not prevent
implementation against synthetic profiles and the defined interfaces. No
production-ready claim follows from completing planning or accepting the design.

## Sources

Repository contracts: [registry](m03a-application-registry.md),
[lifecycle runbook](../runbooks/application-lifecycle.md),
[dispatch runbook](../runbooks/application-dispatch.md),
[credential boundary](m06-fcm-credentials.md).

Primary Firebase documentation checked 2026-11-14:
[HTTP v1 authentication and sending](https://firebase.google.com/docs/cloud-messaging/send/v1-api)
and [send API / validate_only](https://firebase.google.com/docs/reference/fcm/rest/v1/projects.messages/send).
These describe provider behavior; the onboarding states and time limits here are
our proposed product policy, not Firebase guarantees.
