# Onboarding runtime preparation subset

This implements a partial ONB-05 preparation path with immutable delivery plans,
durable scoped capacity reservations, protected worker policy publication and a
one-shot delivery worker preflight. Shared policy deployment and live readiness
acknowledgements remain pending.
The complete protected platform profile in
[the onboarding design](application-onboarding.md#protected-profile-and-approval-file-shapes)
remains required before a runtime adapter can start usable delivery processes.

`onboarding.ParseRuntimePolicy` accepts a closed, bounded operator-only JSON
object with these required fields:

| Field | Constraint |
| --- | --- |
| `version` | Integer `1` |
| `environment` | `staging` or `production` |
| `revision` | Positive integer |
| `delivery_image` | Approved repository pinned by a lowercase SHA-256 digest |
| `network` | Approved Docker network name, at most 63 characters |
| `max_applications` | 1–100 |
| `max_overlap` | 1–`max_applications`, including replacement overlap |
| `max_database_connections` | 1–10000, reserved for this delivery allocation |
| `delivery.memory_bytes` | 64 MiB–4 GiB |
| `delivery.nano_cpus` | 100000000–4000000000 |
| `delivery.pids_limit` | 16–512 |
| `delivery.max_database_connections` | 2–10 per replica |

The configured DB budget must cover all application slots plus replacement
overlap. Existing shared server/admission/scheduler allocations must be reserved
separately in the complete platform budget. An absent capacity configuration
fails closed. M45 serializes host-wide reservations across app-scoped executor
LOGINs; an in-memory capacity check alone is not a reservation. The owner-installed
DB budget must match the protected policy bytes and include existing allocations
before enabling this subset on any host. It is not a fleet inventory service.

The deterministic plan scopes a single delivery replica to canonical app and
configuration UUIDs, environment and a positive generation. Delivery concurrency
and request rate are one; UID/GID are 65532; root filesystem is read-only;
capabilities are dropped and privilege escalation is disabled. It contains no
caller-provided mount, command or environment map.

Shared SERVER, ADMISSION and SCHEDULER acknowledgements are required for the exact
app/configuration/generation/profile revision. Listing these requirements does
not establish that a process loaded that generation or that its source, roles,
mounts, keys and health are approved.

`installation.AppendRuntimeProject` composes the future protected server mobile
policy, preserving existing project objects and key material. It validates both
generations through `LoadRuntime`, rejects project replacement and overlapping
audiences/namespaces, and enforces the protected deployment reader's 64 KiB limit.
Its output contains secrets. No HTTP endpoint exposes it; the future host policy
publisher must enforce authority, immutable publication and restart fencing.

## Protected one-shot preparation

`controlplane prepare-runtime` runs separately from the OAuth verifier, with its
own restricted database LOGIN and host Docker access. No server/BFF route calls
it. Only that trusted host process may receive the fixed `/var/run/docker.sock`;
the HTTP server and ordinary verifier retain their existing authority.

The command reads two fixed protected files through `deployment.ReadSecret`:

- `/etc/hello/runtime-preparation-policy.json`: the complete closed JSON planning
  subset above. M45's immutable capacity profile stores SHA-256 of these exact
  bytes, including whitespace; it is not a hash of secret credential material.
- `/etc/hello/runtime-preparation.json`: the database connection and exact
  app/configuration/setup/generation/epoch snapshot. No caller-supplied path,
  image override, shell command, environment map or mount is accepted.

The settings shape below uses synthetic IDs and a password-free illustrative DSN:

```json
{
  "database_url": "postgres://runtime_executor@db.example.invalid/hello?sslmode=verify-full",
  "application_id": "11111111-1111-4111-8111-111111111111",
  "configuration_id": "22222222-2222-4222-8222-222222222222",
  "environment": "staging",
  "generation": 1,
  "reservation_id": "33333333-3333-4333-8333-333333333333",
  "setup_revision": 3,
  "origin_epoch": "44444444-4444-4444-8444-444444444444",
  "source_epoch": "55555555-5555-4555-8555-555555555555"
}
```

The owner installs M45, an immutable host capacity profile and an app-scoped LOGIN
mapping, then applies `deploy/postgres/onboarding-runtime-grants.sql` to a
dedicated non-owning `hello_onboarding_runtime_executor` NOLOGIN group. The LOGIN
must be restricted and inherit only this group. Neither profile installation nor
production per-app allocation is exposed to HTTP. These are explicit operator
inputs; current code does not automatically manufacture the approved host profile.

The command reserves capacity, checks the exact protected policy digest and
current snapshot, then inspects or creates the deterministic stopped allocation.
The Docker adapter uses Engine API v1.45 on the fixed Unix socket, pinned image,
`/worker`, UID/GID 65532, fixed PATH, network `none`, read-only root, dropped
capabilities, no privilege escalation and bounded CPU/memory/PIDs. No credential
or runtime-policy directory is mounted. A prepared allocation cannot be used as
the runnable delivery generation; completing the full profile requires a
separately reviewed deployment implementation, not manually starting this object.

Same-plan retries reconcile the deterministic name after a lost create response
or concurrent name conflict. Inspection refuses running containers and drift in
scope or supported security settings. It does not inspect arbitrary unrelated
containers or stop/remove any allocation. Caller cancellation and ambiguous
Engine outcomes retain the reservation. Successful output means stopped
allocation only; it does not emit a runtime ACK or change lifecycle/dispatch.

The original reservation permits preparation for 60 seconds and is never renewed
by replay. After expiry, reconcile using the original scope; do not submit a new
reservation to work around uncertain effects. Explicit SQL release requires at
least the original deadline plus 30 seconds **and** trusted absence attestation
after quiescing preparers and reconciling pending daemon effects. The grace period
alone cannot prove a timed-out create failed. No automatic release/cleanup CLI is
provided, and rollback refuses retained reservation/scope/profile history.

## Protected delivery preflight

`controlplane preflight-delivery` uses the same preparation settings and host
profile as `prepare-runtime`, plus the protected input file
`/etc/hello/runtime-preparation-worker.json`. Use the real `delivery_only` worker
schema with one replica, one concurrent request, one request per second per
worker and project, and the exact database connection budget from the host plan.
It requires a same-project FCM credential and the fixed worker-side path
`/etc/hello/fcm/service-account.json`. The DB connection uses `verify-full` TLS.
If the approved DSN names `/etc/hello/postgres-ca.crt`, install its protected
host file at `/etc/hello/runtime-preparation-postgres-ca.crt`. Client TLS key and
certificate paths are excluded from this preflight profile.

The schema owner installs one M46 preflight approval for the app, configuration,
environment, generation and profile revision. It pins SHA-256 of the exact worker
policy bytes and the OID of a dedicated restricted `hello_delivery` LOGIN. Reapply
`deploy/postgres/onboarding-runtime-grants.sql` to grant the preparation LOGIN the
scoped M46 check. The policy digest covers the approved DB DSN and broker policy.
The worker verifies the actual session LOGIN against the approved DSN username.
The owner must point the policy at the intended cluster because the report does
not attest that the executor and worker reached an identical database. The existing
worker group retains shared grants for legacy LOGINs. M47 uses the immutable M46
LOGIN assignment to restrict application rows, route visibility, and the delivery
hold function to that application. Unassigned legacy LOGINs keep their existing
access. This is a row boundary, not live authorization: the worker still needs to
prove the current generation and source before it can run as an onboarded daemon.

Install `/var/lib/hello/runtime` with ownership `root:65532` and mode `0750` on
protected Linux storage. The command validates the worker policy, checks the
live reservation and owner approval, and publishes bytes atomically to
`/var/lib/hello/runtime/<app>/<environment>/<config>/<generation>/worker.json`.
The exact replay syncs directory entries again. A different file at the same
generation fails closed. The credential source is the existing, protected
`/var/lib/hello/credentials/<app>/<environment>/<config>/service-account.json`.

The adapter mounts only those specific files and the optional pinned-path CA read-only.
It uses the profile's approved named network and pinned worker image, UID/GID
65532, a read-only root and the same resource bounds as `prepare-runtime`.
The image must include `/worker --check-delivery-policy`. A host lock serializes
preflight for that generation. The container shares the deterministic allocation
name with the stopped-only mode. Preflight may replace the exact inert, never-started
allocation under the same reservation, after rechecking the owner approval. Docker
removal uses the inspected container ID without force or volume removal. Drifted
allocations still require trusted operator reconciliation. After an uncertain removal,
retry the same preflight; it inspects the deterministic name again. Capacity remains
reserved throughout.

The one-shot worker loads its actual mounted policy, checks the restricted DB
LOGIN, app, registered Kafka source and broker metadata, validates the local
credential, and closes its startup resources. The host requires exit success and
the exact app/digest report from bounded Docker output, then checks the reservation
again. These checks perform no provider or Kafka send. Observing a completed
check attests startup of one process at that time. Activation still requires
authenticated current-generation readiness for the long-running delivery worker
and the shared server, admission and scheduler processes.

M48 provides the database-side acknowledgement lease. The owner assigns each
component a distinct LOGIN and approved policy digest for the reserved generation,
then grants that LOGIN `EXECUTE` on `acknowledge_application_runtime` without table
write access. The function rechecks the reservation, provider configuration, runtime
fences and the session LOGIN; delivery also needs matching M46 approval. A process
instance can renew its 15-second lease, and a different instance can take over only
after expiry. The PostgreSQL fixture exercises this database contract directly.

M49 makes the normal delivery process check its assigned generation at startup.
Apply the migration and reapply `deploy/postgres/daemon-grants.sql` before
upgrading delivery binaries. An assigned worker hashes the exact policy bytes it
loaded and renews its `DELIVERY` lease every three seconds. It waits without
polling or publishing until its generation is current and the app is active.
A failed renewal or loss of current generation cancels and joins both loops;
this does not undo an already-started provider request. Unassigned legacy
workers continue without an acknowledgement lease. The protected preparation
command does not yet launch the normal worker process.

M50 adds heartbeat renewal for shared processes. Grant `EXECUTE` on
`acknowledge_shared_application_runtime(text,bytea,uuid)` to each approved
shared LOGIN; workers must not receive this capability. Admission and scheduler
use SHA-256 of the exact loaded role policy bytes. Protected server mode uses
`SHA256(SHA256(server policy bytes) || SHA256(mobile policy bytes))` (raw digests).
Admission renews only inside a Kafka generation with a verified checkpoint and
at least one assigned partition. The scheduler checks source registration and
broker metadata first; the server loads mobile routes and binds its listener
first. An invalid scope or digest does not renew a lease. Shared processes can
continue serving existing apps while onboarding leases expire. Host publication
and coordinated rollout of the shared policies remain separate work.

## Local evidence

`bash scripts/application-onboarding-runtime.sh` proves actual PostgreSQL role,
capacity, replay, epoch/revision and recovery guards in an isolated container.
`bash scripts/application-onboarding-docker.sh` uses a cached pinned local worker
image, creates and reconciles a stopped allocation and removes only its fixture
allocation. It never pulls an image or starts a worker. The Docker fixture uses a
test reservation guard; these are separate proofs, not a combined DB–Engine–CLI
integration test. Focused HTTP Engine tests cover lost responses, concurrent
create conflicts, malformed responses, cancellation and drift.

Neither fixture proves shared-process policy publication/restart, real mobile
enrollment, Firebase IAM permission, device receipts or dispatch readiness.

For M46, the PostgreSQL fixture also verifies approval isolation, provider
metadata, digest and LOGIN fences, and revocation. The Engine HTTP fixture checks
read-only mounts, immutable-ID start, lost responses and exact report replay.
Separately, the real Linux worker command ran in Docker as UID/GID 65532 with a
temporary restricted LOGIN and the synthetic stack's real Kafka metadata and
credential file. Delivery ledger counts remained unchanged and the simulator
recorded no accepted OAuth or FCM requests.

`bash scripts/application-onboarding-docker.sh --preflight-services` builds the
worker from current source and runs it against isolated PostgreSQL (TLS) and
Kafka (mTLS). It checks the report digest, unchanged lifecycle state and empty
ledger, acknowledgement records and Kafka offsets. The worker has no external
network access. This mode does not run the host CLI.

`bash scripts/application-onboarding-docker.sh --preflight` adds stopped
preparation, policy publication, exact replay and rejection of changed policy
bytes. It requires root on a disposable Linux host with the local Docker socket,
a Docker image store that provides local repository digests, and no existing
`/etc/hello` or `/var/lib/hello`. This combined host sequence has not been verified
on the Darwin development host.
