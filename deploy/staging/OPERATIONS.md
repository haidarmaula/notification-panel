# Single-VM staging operations

This controller targets only `ssh kafka-vps` (`haidar@10.1.16.109:10022`).
Run commands from the backend checkout on the office network/VPN. It does not
require a sudo password; the existing deployment account controls Docker.
The current database is migration **41**, clean. Schema migrations and policy
changes remain separate reviewed operations.

## Commands

```sh
# First install, or atomically update the operations scripts.
python3 deploy/staging/staging.py install

# First-time diagnostics and hourly backup/minutely health scheduling.
python3 deploy/staging/staging.py diagnostics
python3 deploy/staging/staging.py schedule

# Build/check an immutable committed source and deploy it in one command.
python3 deploy/staging/staging.py deploy --ref HEAD --schema 41

# Roll back the most recent deployment, keeping the current compatible schema.
python3 deploy/staging/staging.py rollback

# Recover a process interrupted during replacement or journal finalization.
python3 deploy/staging/staging.py recover

python3 deploy/staging/staging.py health
python3 deploy/staging/staging.py status
python3 deploy/staging/staging.py backup
python3 deploy/staging/staging.py restore-check
```

`deploy --ref` rejects uncommitted runtime source changes. It archives the
specified commit, builds and checks all six hardened images with the existing
`scripts/check-images.py`, and records immutable image IDs and source SHA-256.
The explicit schema value is an operator compatibility assertion against the
already installed database; the command never applies or reverses migrations.
It does not include unrelated uncommitted files, generated credentials or local
`.env`. Repository CI remains required independently; image checks are not a
substitute for unit, integration or vulnerability gates. Hosted CI evidence was
not available to this workstation's unauthenticated GitHub CLI.

The `install` command uploads a complete immutable operations-code directory
and atomically activates it under a lock. A stable launcher resolves that
version before importing modules; cron and active commands cannot mix versions.
The backup recipient certificate is pinned on repeat installs. Never overwrite
its private key or rotate recipients without an independent recovery plan.

## Deployment and rollback safety

The controller first takes an encrypted backup, checks schema compatibility,
verifies the current security settings/readonly policy mounts, and records a
recovery journal. It temporarily sets migration hold and stops/joins delivery,
admission, scheduler and API. In-flight operations may finish while processes
join; a deployment is not a cancellation or a Kafka rewind.

Replacement containers retain the active role policy volumes, host-network
loopback access, readonly root filesystem, UID/GID, resource limits, dropped
capabilities and no-new-privileges. Each candidate must pass actual mTLS
readiness before the temporary hold is cleared. The original containers remain
stopped under rollback names. Failed replacement automatically restores them.
No runtime credentials, mobile encryption keys, provider keys, app dispatch
revision, consumer group, source identity or Kafka checkpoint is replaced.

Every rollback acquires the maintenance fence before touching processes.
Reopening is journaled first, so a crash after successful reopening can be
finalized without stopping services while dispatch is open. Recovery compares
the complete original hold/lineage/schema/dispatch policy state. It refuses to
clear an unrelated incident or operator hold. Inspect the protected journal
and incident state if recovery refuses; never unset a hold blindly.

The controller is deliberately limited to the four known staging roles and
known policy volumes. Changes to role commands, mounts, schema compatibility
or runtime security require review. Old rollback containers/images are retained
for recovery; inspect and prune obsolete releases deliberately, not blindly.
Diagnostics setup has its own protected original-policy backup and resumable
journal; rerunning `diagnostics` recovers an interrupted setup first.

## Encrypted backups and restore validation

The VM creates an encrypted DB/config backup hourly at minute 07. It retains
48 newest snapshots plus one daily snapshot for up to fourteen days. The bundle
contains a consistent `pg_dump` custom archive, count/row-digest evidence from
the same exported database snapshot, four role-policy snapshots, `/etc/hello`
credential/TLS material and the management client material. The envelope uses
OpenSSL CMS AES-256-GCM under a dedicated backup recipient certificate
([OpenSSL CMS cipher reference](https://docs.openssl.org/3.0/man1/openssl-cms/#encryption-and-decryption-options)).

The decryption private key stays only on the workstation:

`~/.config/hello/staging/operations/backup-private.pem` (mode 0600).

Keep a separate secure recovery copy of that key. Losing it makes these backups
unrecoverable. It is never uploaded to the VM, printed or placed in Git.

`backup` also fetches and verifies an encrypted off-host copy under
`~/.config/hello/staging/operations/backups/`. Scheduled VM backups do **not**
automatically reach this Mac or another independent storage service. The Mac
copy is made when `backup`/`restore-check` runs. Independent always-available
storage and key custody still need an owner-selected destination. This is not
continuous WAL/PITR or an agreed production RPO/RTO.

`restore-check` takes/fetches a fresh backup, verifies its ciphertext checksum,
decrypts it locally, and sends the archive over SSH to a disposable PostgreSQL
container. The container has `network=none`, zero host ports, tmpfs data, and
no application, broker or provider credentials mounted. All source table counts
and row digests are compared inside the restore transaction; `sealed=true`
and `migration_hold=true` are set before its only commit. The target is removed
in `finally`. Production/staging data, Kafka and staff sessions are not rewound.
Restoring a backup into active service requires a separate reconciliation of
revocations, external sends, high-water marks and lineage; this drill never
activates a restored database.

## Local health and alerts

Management endpoints use a dedicated mTLS CA and listen only on VM loopback:
server 19101, scheduler 19102, admission 19103, delivery 19104. No public
management ports or provider-based health notifications are enabled.

A minutely user cron job checks all seven containers, role DB/loop readiness,
backlog ages, provider error counter changes, breaker state, backup freshness,
latest ciphertext checksum, available disk and management certificate expiry.
A build uses a separate lock; health checks can continue while images build.
Maintenance mutations serialize on the operations lock; cron skips a busy
maintenance window without changing state.

Records are private under `~/hello-staging-ops/` on the VM:

- `status.json`: latest health snapshot and timestamp.
- `alerts.jsonl`: alert/recovery transitions; also sent to local syslog as
  `hello-staging`.
- `latest-backup.json`, `last-restore-drill.json`: backup and recovery evidence.
- `deployment.json`: current deployment/recovery journal.
- `releases/<id>/image-check.log`: full image-gate build log.
- `failures/`: protected failed-command diagnostics; may contain sensitive data.

No Slack/email/external alert receiver is configured. Local checks cannot notify
you when the VM itself is unreachable; an independent receiver/heartbeat path
is the next operational addition. No notification is sent by health probes.
A controlled `health-drill` temporarily stops the API under maintenance,
requires detection, restarts it, and verifies recovery; it does not send pushes.

Focused safety tests:

```sh
python3 -m unittest discover -s deploy/staging -p ops_test.py
```

## Automatic staging CD

The [staging CD runbook](CD.md) describes the installed outbound-only agent and
GitHub main-only signed authorization. The public repository does not expose a
general Actions runner to this VM. CI must pass before a readiness tag can be
signed; the VM checks its signature, current main SHA and schema before using
this controller's existing backup, maintenance, readiness and rollback flow.
Production has no deployment workflow or environment in this setup.

Hourly backups now wait up to five minutes for the operations lock rather than
silently skipping a run when the minutely health check acquired it first.
