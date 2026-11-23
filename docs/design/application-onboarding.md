# Application onboarding implementation design

Design origin: 2026-11-14. Status updated 2026-11-23: setup, OAuth, activation,
cohort dry-run, release requests and their dashboard flow are implemented locally.
The [checkpoint](../analysis/application-onboarding-implementation.md) records
the remaining production and runtime work. User-approved choices: enrollment before general delivery,
SUPER_ADMIN release after testing, on-premises Docker, protected credential files.
Other values below are engineering defaults, not additional user approvals.

[Product spec](../specs/application-onboarding.md) ·
[ADR-13](../adr/adr-13-application-onboarding.md) ·
[Task plan](../plans/application-onboarding.md) ·
[Proposed OpenAPI](../contracts/proposed/application-onboarding.openapi.json).
The proposed OpenAPI is under `docs/contracts/proposed/`, outside the live-route
contract glob. It is validated explicitly and does not claim mounted endpoints. If
schemas and prose diverge, resolve the conflict before implementing the slice.

## 1. Deployment and trusted inputs

Use three separate control-plane responsibilities:

| Process | Authority | Explicit limits |
| --- | --- | --- |
| Existing HTTP server | Current staff authz, setup metadata, submit bounded commands | No root, Docker socket, secret readback, lifecycle/dispatch issuer rights |
| `cmd/controlplane` (OAuth verification and scoped lifecycle commands implemented; broader reconciliation remains planned) | Reconcile DB operations; separate scoped DB issuer/executor connections; verifier job orchestration | No arbitrary SQL/RPC instructions from HTTP input; never own schema |
| `cmd/provisioner` host service (credential staging and bounded runtime preparation implemented; full runtime execution planned) | Root-controlled file publication and allowlisted Docker lifecycle commands | Fixed local interface, approved images/mounts/resources only; no shell execution from requests |

The provisioner uses a fixed private endpoint with mutual TLS, pinned CA and
separate ingestion/reconciler client identities. Bind only to a host-private
interface; firewall to approved container networks. Private client keys are
root-owned read-only policy inputs. Use TLS 1.3 and no proxies/redirects. The
server identity may stage a candidate, inspect its own stage acknowledgement and
abort an unreferenced stage; only the reconciler may prepare/switch runtimes.
Neither interface accepts a host path, Docker JSON, image tag, environment map,
command string, mount option or arbitrary callback URL. Provisioner authorization
checks DB-backed operation/stage ownership through its own restricted reader,
not merely identifiers supplied by the API. It never assumes a SUPER_ADMIN ID
inside a request is proof of staff authority.

An operator installs a protected platform profile once. It pins environment,
approved image digests, Docker networks, broker/source identities, receipt/address
key-generation policy, DB roles/budgets, resource allocations, mobile identity
profiles and approved greenfield/evidence issuers. Changes use an audited offline
operator command and immutable profile revisions; onboarding HTTP cannot edit it.
This is the explicit platform prerequisite, not a manual step for each app.

Current admission and scheduler processes share an approved source and resolve
application scope from persisted work; they need no per-app replica when the
exact global source and runtime are approved. Delivery remains per-app. Server
mobile trust is immutable at startup, so adding an app requires a protected merged
policy generation and restart acknowledgement. Preparation must collect explicit
SERVER, ADMISSION and SCHEDULER acknowledgements for the intended scope rather
than create redundant daemons or infer readiness from an app UUID parameter.
Cap onboarded runtime slots at the operator-configured budget;
no configured capacity means `RUNTIME_CAPACITY_UNCONFIGURED`, not unlimited.
Keep delivery one replica/concurrency one and request/project rates at most 1/s
initially, subject to explicit approved shared-project/host allocations. Profile
capacity must include overlap during replacement and aggregate DB connections.
These are pilot limits, not throughput promises. Never hard-code staging app,
project, Kafka incarnation or credentials into the new adapter.

## 2. Protected files, ingestion and recovery

Host root: `/var/lib/hello/credentials/<app_uuid>/<environment>/<config_uuid>/`.
All components derive from validated canonical IDs or fixed environment enums.
Use protected directory permissions (0750 root/authorized worker group), regular
0640 root-owned credential files and no extended ACLs. Read-only bind mount only
the approved version directory into the app worker/verifier at `/etc/hello/fcm`.
Do not mount the app parent, host credential root or Docker socket. Existing
shared UID/GID 65532 is acceptable only with disjoint mounts/process authority.
Credential storage and every durable copy must live on verified encrypted host
storage. Default Docker volume location is not presumed encrypted.

Reuse `deployment.ReadSecret` and `fcm.NewFile` without weakening their current
checks. Provisioning uses directory-relative file operations that reject symlinks
and substitutions, restrictive creation permissions from the start, no overwrite,
file fsync, atomic publication and parent-directory fsync. A candidate is immutable;
a new upload gets a new UUID/version. No plaintext material in shell arguments,
normal temporary directories, images, environment variables, DB rows or queues.

Upload transaction protocol:

1. Authenticate/recheck staff and request shape; read setup revision without
   holding a transaction across network calls. Compute canonical payload HMAC
   with a deployment-scoped fingerprint key; never use an unkeyed secret hash.
2. Reserve an ingestion UUID in a short DB transaction, with actor/session,
   app/revision, key/fingerprint and expiry. It is not an accepted candidate yet.
3. Stream the bounded credential to the authenticated provisioner. It verifies
   reservation and identity, strictly validates JSON locally, writes a provisional
   immutable file, and returns opaque config/stage ID plus safe metadata. No OAuth
   call is necessary here. Repeating the same reservation converges; mismatched
   bytes/identity fail. Two requests cannot own the same stage.
4. In a fresh transaction recheck current staff/session, app/setup revision and
   reservation; insert config reference, update desired candidate, audit and
   finalize idempotency together. Return 201 only after commit. The upload bytes
   are never needed by subsequent durable operations.
5. Commit acknowledgement loss is recovered from DB idempotency, not by creating
   another candidate. Abandoned stages expire after 24 hours; garbage collection
   checks DB references, live leases and runtime mounts before deletion. If DB
   cannot be checked, retain the file and report cleanup backlog.

Competing writes may fail 412 after staging; cleanup is asynchronous. A failed
replacement does not alter the current version. Abort/GC is never a command to
remove a current/retained credential. Ingestion reservation retries use the same
key for up to 24 hours; if its bytes were never durably written, the caller must
re-upload. This is explicit, not a guarantee that lost upload bytes can be recovered.
The BFF/proxy disables body logs and disk buffering for this route or places any
required buffers inside the same protected ingestion boundary.

Maintain a copy inventory (original source, candidate, mounted current, retained
rollback and encrypted backup). Do not delete staging's existing source file
until adopted references and recovery procedures are reconciled.

## 3. Enrollment, cohort, approvals and management API

The complete proposed paths/payloads are in OpenAPI. Every write uses the setup
ETag plus UUID idempotency key and rechecks current DB SUPER_ADMIN authority.
List profiles and approvals returns at most 100 items, deterministically by ID;
the operator profile must enforce that bound rather than silently truncate.
All new endpoints reject query parameters. After any accepted async write,
clients GET readiness again for the next setup ETag; a 202 operation response
is not a fresh setup snapshot. Existing registry app creation keeps
its body and business-grant behavior; create the setup row/revision atomically
for new apps. Legacy apps receive a held setup row through migration but cannot
use greenfield delegation without adoption evidence.

### Enrollment profiles

`GET /onboarding/enrollment-profiles` lists only safe available template IDs and
supported platforms. `PUT /onboarding/enrollment` selects a profile UUID and one
or two distinct platform entries: platform ANDROID/IOS and its package/bundle
`application_identifier` (1..255 bytes, no controls). Profile policy verifies the
identifier against the external broker's approved integration; it is not accepted
as proof of app authenticity. FCM project, package and origin identity are separate.

Templates pin mobile issuer, purpose, Ed25519 verification key, origin issuer/key,
receipt observation policy and external subject-mapping adapter. Derive distinct
mobile audience `hello-mobile:<app_uuid>:<environment>` and app-specific namespace;
derive a distinct origin audience too. No caller-selected issuer, JWKS/URL or
signing key. External broker must support these derived values; absent readiness
or subject mapping is `ENROLLMENT_NOT_CONFIGURED`. Enrollment profile selection
is allowed only during PROVISIONING before first activation; changing trust for
an active app is separate reviewed migration work.

Routes are generated by trusted policy, one per (app, platform,address_type),
using explicit `FCM_REGISTRATION_TOKEN` initially. Resolve provider namespace/route
keys inside the profile and verify exact delivery/installation agreement. FID
support remains existing explicit policy, not a new default. Do not infer a token
namespace from its string. Server mobile policy is immutable: compose a new full
policy generation, preserve other apps, stage it, replace/join the server process
and verify readiness before activating the app. A shared-server replacement must
be serialized and retain its existing grants/receipt keys; report the brief
maintenance window. Never pretend this supports hot-loading trust material.

### Test cohort and evidence

`PUT /onboarding/test-cohort` accepts 1..10 distinct scoped installation UUIDs,
a reason and approved `evidence_id`; it queues TEST_COHORT. It checks ACTIVE/HELD,
current enrollment/config, live active owners, consent, binding epochs and approved
test designation; all installations must belong to this app/environment. Resolve
bindings server-side. Generate a new cohort UUID and scoped M37 route approvals
using reviewed delegation; superseding a cohort invalidates prior permission/test
and release evidence. API cannot submit arbitrary tokens, route destinations or
evidence digests. A route approval enables classification, not dispatch.

`GET /onboarding/approvals` lists safe preapproved TEST_COHORT/PILOT/FULL bundle
IDs for this app and epochs. An operator imports immutable approval bundles using
a protected host CLI (`cmd/controlplane approvals import --file <protected-file>`,
planned); file contains app/profile versions, epochs, exact cohort/population,
reviewed consent, legacy exclusion, quota and coverage artifacts/digests, issuer,
expiry and intended destination. TEST_COHORT approval binds the exact installation
IDs and current binding epochs before its new cohort UUID exists; PILOT/FULL
approvals bind the resulting cohort UUID and revision. Verify the signed manifest against an offline
pinned evidence-issuer public key, then issue immutable DB evidence under a
restricted issuer identity. Store public evidence metadata only; never trust
HTTP-supplied attestation as reviewed evidence. Initial app creation provenance
and greenfield platform authorization can replace legacy-writer inventory only
for provably new apps, not imported projects. Consent/coverage are not implied by
creation or enrollment. Expired/revoked bundles are visible but unusable.

This explicit approval import is an operational prerequisite when external consent,
quota or coverage cannot be established automatically. Credential setup and ordinary
activation need no per-app SQL/mount/restart. The first release still requires
reviewed evidence; the dashboard shows exactly which approval is missing. Automating
that evidence source is deferred, not concealed behind an apparent one-click release.

### Release and test success

POST `/onboarding/dispatch/release` queues PILOT or FULL with cohort/evidence UUIDs.
PILOT requires a configured test cohort, target-permission dry-run PASSED within
15 minutes and reviewed approval, but no successful real test yet. This permits
the first bounded test. It remains limited to the approved cohort: no broadcast.
The admin sends through existing draft-campaign creation and scoped campaign
send endpoints, with current business grant/send permission; no direct provider
send endpoint and no management permission converted into business authority.

FULL requires a current PILOT and successful test evidence for every enabled
platform: recorded provider acceptance plus authenticated DELIVERED or OPENED
receipt for a designated test installation, with server receipt time within one
hour. Resolve these facts server-side from the existing campaign/delivery/receipt
records and freeze their IDs/config version/binding epochs/current pilot generation
in the approval. Receipt proves client observation, not human visibility. The
mobile client must implement the existing receipt protocol; no fabricated manual
success flag. Tests submitted before rotation/cohort changes cannot authorize
FULL. All coverage/consent/quota guards continue to apply to real campaigns.

POST `/onboarding/dispatch/hold` is a synchronous DB-only incident command. It
sets HELD, advances the dispatch generation even if already held, clears approval,
invalidates pending release/promote completion and returns readiness. It must not
wait on Docker/provider connectivity. It reuses the guarded one-way hold mechanism;
it cannot unseal the global runtime or change application lifecycle status.

## 4. Schema and authority design

Use BIGINT internal app/staff IDs, UUID public identities and operation IDs,
TIMESTAMPTZ timestamps, BIGINT positive revisions represented as strings in JSON.
Do not allocate a migration sequence number until implementation; add paired
six-digit migrations before sqlc generation. Suggested entities:

| Table | Key columns / constraints |
| --- | --- |
| application_onboarding | app_id PK/FK, environment nullable until first FCM upload, revision >0, profile_id, candidate/current_config_id, runtime_generation, cohort_id; current belongs to app/environment; environment immutable once set |
| application_provider_configurations | id UUID PK, app_id FK, version >0 unique per app, environment, target_project, service_account_email, opaque_file_ref unique, state, created_by snapshot, created_at; immutable identity/ref; composite unique(app_id,id) |
| application_onboarding_ingestions | reservation UUID, app_id, actor/session/generation, expected_setup_revision, HMAC fingerprint+key_version, idempotency scope/key, stage ref, expires_at, state; unique actor/app/route/key reservation |
| application_onboarding_operations | id UUID, app_id, kind/state, actor/session/generation, idempotency scope/key/fingerprint, config/cohort/evidence IDs, expected setup/app/dispatch revisions, epochs, lease owner/token/expiry, attempt count, not_before, result revision/code, timestamps |
| application_provider_checks | app_id/config_id/check-kind plus check UUID, outcome, project/environment/epochs, binding/route versions if applicable, checked_at/expires_at; no token or provider body |
| application_runtime_acknowledgements | app_id/component/generation PK, config/profile/policy digest, epochs, executor identity, lease expiry and observed readiness; never accept a bare HTTP heartbeat |
| application_onboarding_approvals | id UUID, app_id, kind, issuer/key version, signed manifest digest, cohort/config/profile/epoch/revision scope, artifact references/digests, expires/revoked/consumed_at; immutable evidence |

Composite FKs `(app_id,config_id)` and equivalent operation/cohort references
prevent cross-app references in DB, not just handlers. Runtime current pointer is
one row per app, changed only by fenced commit. One active mutation lease per app;
allow verification of a pending version without switching the current pointer.
New setup/candidate/evidence history is retained on staff deletion: attribution
is a snapshot, not a cascading delete of security history. Revocation makes
pending work unusable even if staff rows later disappear. Pending operation actor
session/family references must preserve enough immutable evidence for that check.
Indexes: operation queue(state,not_before), app/created_at/id history, unique
idempotency namespace, candidate app/version and live ingestion expiry. List/read
responses are bounded; no unbounded operation scan in readiness.

Do not alter existing immutable M37/M38/M39 history. Introduce narrow delegated
DB entry points and amend protected guards to recognize only those authenticated,
immutable onboarding grants. API has EXECUTE only on request/metadata functions,
not lifecycle/dispatch application functions or protected tables. Separate
NOLOGIN groups and distinct restricted LOGIN issuer/executor identities for
lifecycle, route and dispatch. Runtime logins cannot SET ROLE into them. Fixed
security-definer search_path, revoked PUBLIC EXECUTE, restricted function owners,
no dynamic SQL from input and role-inheritance/effective-privilege checks are
mandatory. Owner setup is a reviewed install step, not API startup migration.

| Proposed DB capability | Actor / bounds |
| --- | --- |
| request_onboarding_operation | API; record authenticated actor/session and desired command, no protected transition |
| issue_onboarding_lifecycle / apply_onboarding_activation | Distinct lifecycle issuer/executor; exact operation, actor/session, profile, config, prepared acks, epochs/revisions; PROVISIONING→ACTIVE only, atomically set initial current config |
| issue_onboarding_route / apply_onboarding_test_cohort | Distinct route issuer/executor; scoped live binding/consent/evidence and route revision; no general route override |
| issue_onboarding_dispatch / apply_onboarding_release | Distinct dispatch issuer/executor; PILOT/FULL prerequisites, signed approvals, test evidence and exact generations |
| hold_onboarding_dispatch | Authorized management/incident path; one-way hold, no release privilege |
| commit_onboarding_promotion | Restricted executor; dispatch already held, drained starts and current exact operation; switch only, never release |

These are proposed names, not callable functions today. Grant maximum lifetime
is 15 minutes; apply rechecks real DB clock, role separation, actor's active staff
session/current SUPER_ADMIN generation, immutable evidence, application/setup/
dispatch revisions and origin/source epochs. Recheck explicit grant/send rights
only for actual campaign sending. Revoked staff or stale session supersedes pending
management work. A successful historical retry returns its original outcome and
cannot reenable anything after a hold. Greenfield registration uses a platform
profile ID and schema-maintained creation provenance, not a client flag.

Serialize all new app control mutations on the app row. Standard new path lock
order: runtime SHARE → app NO KEY UPDATE → staff IDs ascending → staff session
lineage → onboarding row → dispatch policy → operation/evidence rows ordered by
UUID → affected installations/bindings in existing lifecycle order. Acquire any
existing lifecycle/route/dispatch grant locks only while the same app lock is held.
Existing M38's grant-before-policy and M39's policy-before-grant ordering remain
unchanged; do not call them under an inverted prelock. Review FK and staff-session
locks against current helpers before implementing each query. Concurrent legacy
apply, staff revocation/deletion, route reassignment, hold and new apply must be
covered by real PostgreSQL deadlock/rollback tests. Use READ COMMITTED, lock_timeout
2s and short DB transactions; no filesystem/network I/O under locks.

Register persisted audit labels centrally: APPLICATION_FCM_CONFIGURE,
APPLICATION_ENROLLMENT_CONFIGURE, APPLICATION_FCM_VERIFY,
APPLICATION_ENROLLMENT_ACTIVATE, APPLICATION_TEST_COHORT_CONFIGURE,
APPLICATION_DISPATCH_RELEASE, APPLICATION_DISPATCH_HOLD,
APPLICATION_FCM_PROMOTE and APPLICATION_ONBOARDING_OPERATION_COMPLETE. Use app
scope, actor, config/operation IDs, revisions and safe outcome codes only. Operation
completion audit and transition commit together; request audit is not success.

## 5. Leases, rotation and operational defaults

| Concern | Default / behavior |
| --- | --- |
| Readiness ETag | Strong opaque setup revision; dependent facts include source-specific revisions and freshness; reads never pretend it is a global snapshot lock |
| Idempotency | 24h minimum for completed requests; pending records never expire; same actor/app/route/key and canonical HMAC body must match |
| Operation claim | DB-clock 60s lease, renew 15s, monotonic attempt token; expired executor cannot commit or launch later stages |
| Verification | 60s total, at most 3 bounded network attempts; max 5 new jobs/app/min; one per config; 15min evidence |
| Runtime preparation | 5min attempt deadline, max 3 reconciliations per operation with bounded backoff; exhaustion fails safely and leaves held |
| Runtime acknowledgement | 60s validity, renew 15s; must match all required components, versions and epochs at commit |
| Stage cleanup | 24h grace; fail closed if references/leases/mounts cannot be checked |
| Old credential retention | 7 days after drain, then reference-checked removal; incident compromise overrides retention and requires provider-side revocation |
| Operation/check metadata | Retain 90 days minimum; immutable applied approval/audit history follows existing security retention and is never auto-truncated |
| Cleanup metadata | Preserve compact idempotency tombstones for the advertised window and all live references; HMAC key versions retained as long as replay matching needs them |
| Backup | Encrypted backup at least hourly or more often; automatic off-host transfer and age alert; pilot target RPO <=1h, RTO <=4h must be demonstrated before production acceptance |

No auto-FULL after restart, rotation or rollback. Promotion first advances dispatch
to HELD, stops new physical starts and drains/reconciles known attempts. Stage the
new worker and full server receipt policy, await fenced acks, atomically switch
current config/generation, then require new permission/test/release. On failure,
keep HELD, restore the prior trusted process generation only under a fresh fenced
operation; never rewind dispatch or source epochs. The default seven-day retention
is local recovery policy, not permission to leave a known leaked Google key active.

Current staging's encrypted backup implementation is reusable, but hourly copies
on the same VM are not independent recovery. Extend its runner to an operator-
configured separate destination and monitor receipt there. Encryption/unlock and
backup private keys are held off-host by named operations custodians, with tested
separate recovery copies. No selected destination or custodian means production
blocked, not an instruction to invent addresses or put the key beside the backup.
Record file access audit and redaction verification for all ingestion/helper paths.
Schedule backup plus transfer with enough margin to satisfy the one-hour RPO;
one hourly local snapshot alone does not prove that bound.

### Protected profile and approval file shapes

Version these operator-only JSON objects explicitly; they are not HTTP payloads.
`platform-profile-v1` requires `id`, `revision`, `environment`, `image_digests`,
`networks`, `database_role_refs`, `broker_source_refs`, `resource_limits`,
`enrollment_profiles`, `evidence_issuer_keys`, `greenfield_policy_id` and
`credential_storage_id`. The secret/public-key references resolve from protected
local files. No credential JSON in the manifest itself. Each enrollment template
requires issuer/purpose, public verification-key ref, origin issuer/key ref,
audience/namespace derivation version, allowed package/bundle IDs, subject-mapping
adapter ref, supported platforms/address types and receipt profile. Reject unknown
fields, duplicate IDs, weak keys and unsupported derivation versions. Signed-envelope v1 has exactly `version` (integer 1), `issuer_key_id`, `payload`
and `signature`; the last two are unpadded base64url strings. Sign the exact UTF-8
payload bytes with Ed25519; verification authenticates those bytes before strict
JSON parsing, with no deserialize/reserialize step. Hash those same payload bytes
for the evidence digest. Resolve issuer_key_id only against the protected pinned
key map, never an embedded key or URL; the signed payload must repeat and match
the issuer ID. Reject unknown fields, duplicate keys, invalid UTF-8 and oversized
manifests (64 KiB decoded payload). This avoids ambiguous canonicalization across
runtimes; different byte encodings are different immutable evidence objects.

`approval-v1` requires `id`, `kind`, `app_id`, `environment`, `profile_revision`,
`configuration_id`, `origin_epoch`, `source_epoch`, `expected_application_revision`,
`expected_dispatch_revision`, `scope`, `artifact_digests`, `issued_at`, `expires_at`,
`issuer_key_id`; the signed envelope carries its signature. Scope is kind-specific: TEST_COHORT uses
installation/binding IDs and epochs; PILOT/FULL use cohort ID/revision and permitted
population. FULL additionally references current test campaign/delivery/receipt IDs
for every enabled platform. Expiry is at most 24 hours from issue; delegated DB
execution grants last at most 15 minutes and never outlive the bundle. Evidence
is rechecked against current DB facts, not accepted on signature validity alone.
Signed manifests contain metadata/evidence references, not secrets or raw tokens.

## 6. Scope, rollout and completion

Backend onboarding, restricted provisioner/control-plane, proposed API/schema and
integration tests are the initial implementation scope. Dashboard wizard follows
as a separate slice consuming the same API. Test-only keyless/file transports and
simulated provider environments remain injection-only. Existing imported staging
app is not silently greenfield: preserve and adopt its actual protected policies
and offline approvals with a dedicated reviewed migration, not its UUID in code.

Implementation may start with synthetic/local fixtures and disabled capabilities
once this plan is accepted. Enabling staging/production is a separate operational
action. No live credential or DB transition is authorized by this planning work.
Production gates in spec section 9.1 remain mandatory even if all unit tests pass.
The only outstanding deployment inputs are named custodian/backup destination,
encryption/unlock mechanism, signed platform/evidence profiles and measured
capacity. They block live enablement, not coding against the defined interfaces.
