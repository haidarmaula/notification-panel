# ADR-13 — Application onboarding and managed FCM credentials

Date: 2026-11-14. Status: **protected-file approach accepted on 2026-11-14; implementation authorized; setup/OAuth implemented locally;
full lifecycle/runtime work remains**.
Scope: [application onboarding spec](../specs/application-onboarding.md).

## Context

The registry creates PROVISIONING applications. Enrollment activation and dispatch
release use protected offline DB procedures. FCM credentials and per-app policy
are deployment-mounted, and each delivery worker loads one immutable app policy.
Consequently the product cannot complete ordinary new-app setup from the panel.

A service-account upload is a new secret-ingestion boundary. It cannot be passed
to the existing file factory as an arbitrary path or treated as proof of IAM,
mobile readiness, route ownership, quota or dispatch approval. Mobile enrollment
requires ACTIVE, so requiring a device before activation creates a dependency cycle.

## Decisions and engineering proposal

### 1. Keep lifecycle, provider setup and dispatch separate

Preserve ACTIVE as enrollment/business eligibility. Expose actionable onboarding
checks alongside it; activate enrollment with dispatch held, then enroll devices,
review routes, run pilot and authorize full coverage. Do not redefine historical
ACTIVE rows or add an unguarded status PATCH.

### 2. Extend the existing protected-file credential source

The user selected protected per-app credential files on the on-premises VPS,
with restricted provisioning, version isolation, rotation and production gates.
PostgreSQL stores only opaque references and safe metadata. OpenBao or another
secret manager is optional future work; encrypted database credential blobs are
not the selected design.

Reuse `fcm.NewFile`, `deployment.ReadSecret` and their current strict identity,
size, ownership, mode, symlink and ACL checks. A restricted host-side provisioner
validates uploads and creates immutable app/environment/version files and volumes.
It derives paths from trusted identifiers, never caller-supplied filenames or
paths. Write under protected ancestry, set root ownership and worker-readable
0640 file permissions, flush and atomically publish before recording a usable
reference. Never overwrite the live credential file. Reject symlinks and path
substitution during creation, not only on later reads.

The HTTP server forwards a bounded credential input through an authenticated
restricted interface; it receives only an opaque reference and safe metadata.
It has no root access, Docker socket, general filesystem-write endpoint or read
access to stored credential files. Durable work contains references only; no
private key in DB/outbox/retry payloads. The worker/verifier receives only its
approved app/version via a read-only mount, retaining immutable configuration.
Shared container UID/GID alone is not app isolation; mount and deployment
restrictions must prevent access to any other app's files or volumes.

Provisioning and DB commit require recoverable provisional references and orphan
cleanup. Document all retained source/volume copies and delete obsolete copies
only after recovery/retention checks. Never expose secrets in errors, audit,
telemetry, uploaded filenames or temporary files outside protected storage.
Preserve fixed Google destinations and bounded OAuth/transport behavior. Keyless
credentials remain future explicit work without ambient ADC fallback.

Disk encryption and its unlock/key custody, file-access auditing, least-privilege
Google IAM, rotation/revocation and tested off-host encrypted backup are production
requirements. Protect backup decryption keys independently. Host root/Docker
administrators remain trusted: files and a secret service on the same compromised
host do not remove that threat. Design acceptance does not establish readiness.

### 3. Reconcile immutable runtime generations

An authenticated management write records desired state and durable work. A
separate platform reconciler prepares trusted per-app immutable policy and worker
process generations, reports fenced acknowledgements and executes narrow lifecycle
commands. Keep one-app workers initially. This requires a deployment adapter and
capacity policy. Use a host-side Docker deployment adapter based on staging;
its restricted command interface and lifecycle are specified in the
[implementation design](../design/application-onboarding.md).
Per-process configuration remains immutable. Restarting/replacing a generation is
an orchestrated effect, not mutating loaded config or rereading key files on send.

Admission, scheduler, Kafka/source identity and receipt/enrollment policy are part
of readiness. A delivery worker acknowledgement alone is insufficient. Existing
attempt records remain authoritative across a generation replacement.

### 4. Delegate narrowly at the database boundary

The API keeps no lifecycle/dispatch issuer/operator privileges. A new restricted
execution path must validate app identity, operation kind, expected revisions,
origin/source epochs, authorized actor, approved platform policy and deduplication
inside PostgreSQL. Separate lifecycle and dispatch capabilities even if one
reconciler coordinates them. An incident hold invalidates pending release work.

Restrict initial delegation to provably new apps under approved greenfield policy.
Retain offline review for imported/migrated apps until explicitly adopted. Do not
fabricate consent, legacy-fencing evidence or full-coverage approval from empty
arrays. Direct runtime writes to protected columns remain denied. Planned SQL capabilities and lock ordering are specified in design section 4.
Their implementation requires security review and real-role integration tests
before this path can be enabled.

### 5. Treat verification and rotation as versioned evidence

OAuth, target permission, runtime readiness and device receipt are independent.
Checks bind to exact versions and scope. Candidate verification never changes a
working current credential. Promotion fences new starts, holds dispatch, drains
old work, switches the immutable generation and requires fresh release. An
uncertain submission is recovered using its original evidence, never resent merely
because promotion or acknowledgement failed.

## Alternatives considered

- Status-only activation endpoint: cannot prepare runtime, protect secrets or
  establish delivery readiness; would bypass current protected authority.
- Plaintext JSON in application/config rows: expands credential exposure to
  ordinary queries, exports, logging and backups.
- Encrypted DB blobs: possible with external KMS and envelope encryption, but
  adds key/restore/rotation responsibilities; requires a separate reviewed design
  if proposed later instead of the selected protected-file boundary.
- Dedicated secret manager (including OpenBao): optional future integration;
  adds service operation and unseal/recovery duties and is not required for this scope.
- One shared dynamically mutating multi-app worker: larger isolation and lifecycle
  change than required; defer until evidence warrants it.
- Keep per-app manual deployment forever: preserves current controls but fails
  the intended product onboarding experience.

## Consequences and compatibility

Platform setup becomes more involved once; ordinary greenfield app setup becomes
an auditable product workflow. Filesystem/DB atomicity requires reconciliation,
orphan cleanup, version fences and restore procedures. Existing file-mode and
offline paths continue to work and remain covered by their tests.

When implemented and accepted, this ADR amends ADR-04 A-2's worker-only credential
boundary for the isolated upload/verifier path and the deployment-only source
contract in M06. It also adds a delegated alternative to the offline lifecycle
and dispatch runbooks. None of those changes are effective merely because this
proposal exists. Existing authority, scope, idempotency and recovery rules persist.

## Confirmed product and hosting constraints

On 2026-11-14 the user accepted enrollment activation before general delivery and
SUPER_ADMIN-only explicit delivery approval after testing. Hosting is an office
on-premises VPS, with staging accessible through `ssh kafka-vps`. After VPN activation and SSH-agent setup, read-only inspection succeeded.
Staging uses separate Docker containers for server/admission/delivery/scheduler,
with non-root UID/GID 65532, read-only roots and distinct read-only policy volumes.
No Compose project was listed. The proposed deployment adapter should preserve
this isolation and use host-side deployment authority, never a Docker socket in
the API server. Follow-up metadata inspection confirmed the source and delivery-volume FCM files
are regular files, root-owned, group 65532, mode 0640. Repository provisioning
code installs these files and backup code encrypts credential/policy bundles.
Actual disk encryption, access auditing, key rotation and off-host recovery remain
unverified. No secret contents were read or deployment configuration changed.
These confirmations accept the storage direction, not all detailed contracts.

## Deployment acceptance inputs

The design and task plan define the implementation interfaces and defaults.
Operators still supply disk encryption/unlock and backup-key custodians, the
independent backup destination, signed enrollment/evidence platform profiles,
measured capacity and protected provisioner identities. These are live-enablement
gates, not unresolved API/schema choices. Use synthetic fixtures during coding;
never invent production values. Secret storage is selected; no secret-manager
installation or runtime change is authorized by this document.
