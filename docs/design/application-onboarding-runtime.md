# Onboarding runtime preparation subset

This implements a partial ONB-05 preparation path: immutable delivery plans,
durable scoped capacity reservations and stopped Docker allocations. It does not
publish executable runtime policies, start containers or acknowledge readiness.
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
