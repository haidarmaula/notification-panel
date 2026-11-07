# Building and running the production commands

Build a role locally with `docker build --target server -t hello-server:local .`.
The same Dockerfile has worker, scheduler, bootstrap and requeue targets. Each
image contains one static Go executable and the builder's CA bundle. The official
Go 1.26.10 Alpine builder is pinned by OCI index digest. Its Go toolchain cannot
auto-upgrade. The build verifies module checksums and compiles in readonly module
mode. The build context includes Go source and module manifests. Development
hot-reload tools and local policy files stay outside the context.

The runtime starts as UID:GID `65532:65532` with an exec-form entrypoint so the
command handles SIGTERM directly. Use a read-only root filesystem, drop Linux
capabilities and enable no-new-privileges. Mount only the files for that role
as read-only, root-owned files accessible to its process group, for example
mode 0640, owner 0:65532. Parent directories must be root-owned, traversable and
free of group/other write bits. Extended ACLs, symlinks and filesystems without
the required ACL inspection support are rejected. Ensure the secret manager's
actual mount mechanism meets those checks. Keep broker/database access private,
publish HTTPS behind the approved TLS ingress and restrict delivery worker egress
to its verified DB, Kafka and Google endpoints.

The server and bootstrap image entrypoints include `--deployment`. A required
policy is read from the fixed path below, with no .env or environment fallback.
Darwin uses `/private/etc/hello` for protected policies.

| Role | Policy and required fields |
|---|---|
| Server | `/etc/hello/server.json`: `database_url`, `api_key`, `access_secret`, `refresh_secret`, `mobile_policy_file`. Use distinct random values for the three auth secrets, each at least 32 bytes. Use an empty mobile path to keep optional mobile routes disabled. A nonempty path points to a second protected JSON file (64 KiB maximum) containing the application's mobile identity, installation and optional receipt policies. |
| Bootstrap | `/etc/hello/bootstrap.json`: `database_url`, `admin_name`, `admin_email`, `admin_password`. The one-shot password is 8–72 bytes. Mount this only for provisioning, remove it afterward and rotate the login credential as required. |
| Worker | `/etc/hello/worker.json` selects admission or application-specific delivery, as described in the [delivery worker runbook](delivery-worker.md). |
| Scheduler | `/etc/hello/scheduler.json` uses the scheduler's approved send source and authenticated broker policy. |
| Requeue | The operator job uses its own reviewed [replay policy, grant and execution procedure](operator-replay.md). |

Role policies accept exact field names and reject missing, unknown or duplicate
members. Use explicit PostgreSQL host, port, database, service account and
percent-escaped credentials with `sslmode=verify-full`. Optional TLS auxiliary
files must also pass ownership, mode and ACL checks. PG* and TLS trust override
variables are rejected. The protected DB pool uses two connections per process.
Account for all replicas and other services when comparing DB connection demand
with PostgreSQL capacity. Select CPU, memory, replica counts, request-rate
allocations and drain deadlines from staging measurements.

Use a finite maintenance job for schema migrations, with credentials separate
from runtime roles. Verify the migration version and deployment-specific
preflight before starting the new binaries. These images contain no migration
runner. Promote the measured image digest after vulnerability scanning, registry
and signature verification, reviewed private TLS/ACLs and application activation
approval. Roll back via a compatible prior image and the applicable schema
runbook. Guarded down migrations can intentionally refuse removal of durable
delivery or receipt evidence.

To check entrypoints offline, run each target with `--network none --read-only
--cap-drop ALL --security-opt no-new-privileges`. Worker, scheduler and requeue
accept `--help`. For server and bootstrap, mount synthetic role policies owned by
`0:65532` with mode 0640 and point the database URL at a closed local port.
The commands should reach their database startup check and exit with failure.
Changing a policy to mode 0660 should instead fail at configuration loading.

Related decisions: [transport topology](../adr/adr-07-kafka-topology.md),
[deployment security](../adr/adr-11-security-baseline.md) and
[process lifecycle](../specs/process-lifecycle.md).
