# Automatic staging CD

Every push to `main` runs the existing **Backend CI** workflow. Deployment is
eligible only after all seven named jobs succeed: quality, simulated delivery,
full integration/sealed recovery, six image checks, monitoring/PITR, reachable
vulnerability scanning and the aggregate release-readiness gate. PRs, failed or
cancelled runs, schedules and manual runs cannot authorize deployment.

## Trust and flow

This repository is public. Staging does not run a self-hosted Actions runner,
accept a public webhook or store a GitHub token. All GitHub jobs stay hosted;
the VM makes outbound, read-only HTTPS Git fetches.

1. `.github/workflows/staging.yml` receives successful `Backend CI` completion
   events for an original-repository push to `main`.
2. The hosted publisher re-reads that exact CI run/attempt and all seven job
   conclusions from GitHub. It confirms that the SHA is still current `main`.
3. The `staging` environment permits only the main branch. Its dedicated Ed25519
   secret signs the repository, SHA, schema compatibility and CI run/attempt.
   An annotated `staging-ready/<sha>` tag carries this canonical signed metadata.
4. The VM's minutely CD job fetches main and readiness tags. It verifies the tag
   target and signature against a separately pinned public key. It accepts only
   the configured repository and clean schema 41. The committed migration maximum
   must match that reviewed schema on both publisher and receiver. It suppresses stale completions
   and cannot replace a newer deployment with an older CI authorization.
5. The existing image gate builds/checks that exact committed source. Immediately
   before replacement, the agent checks remote main again under the deployment
   lock. Superseded images are not deployed.
6. It takes an encrypted backup, uses the existing guarded maintenance fence,
   replaces the four roles, verifies mTLS readiness and restores prior hold state.
   Failed readiness uses the existing rollback path. Consumer groups, credentials,
   keys, protected policy volumes and one-device pilot scope are retained.

The image build uses its own lock; health checks continue while it runs.
Deployments serialize independently of GitHub workflow cancellation. A repeated
poll does not redeploy an already committed SHA; crash recovery can adopt a
matching committed deployment without replacing it again. Failed CD attempts
retry after five minutes, up to three attempts per SHA. Inspect/recover a failed
maintenance journal before retrying; no incident hold is automatically cleared.
A new main commit provides a new bounded attempt budget.

## Authorization is not a deployment result

The hosted job uses `environment: {name: staging, deployment: false}`. It can use
staging secrets/variables but does not create a misleading successful GitHub
deployment record before the VM finishes. Its green status means authorization
was published. Actual replacement/health results are recorded on the VM:

- `~/hello-staging-ops/cd/status.json`: authorized SHA, CI run, attempts, release,
  and deployment result.
- `~/hello-staging-ops/cd/last-deployed.json`: last successful CD deployment.
- `~/hello-staging-ops/cd/last-error.json`: latest polling/authorization failure.
- `~/hello-staging-ops/cd/poll.log`: bounded-operation diagnostics.
- `~/hello-staging-ops/deployment.json`: underlying deploy/rollback journal.

Local health checks report CD failures through existing status/syslog alerts.
External alert delivery is still separate. The poll interval is one minute;
image build/check time follows CI completion. Nothing sends a push notification
as a health test.

## Installation and custody

```sh
# Authenticate locally; do not paste credentials into files or chat.
gh auth login --hostname github.com --web --git-protocol ssh

# First-time GitHub staging environment and signature trust provisioning.
python3 deploy/staging/cd_setup.py

# Install immutable trusted operations code and add the CD cron entry.
python3 deploy/staging/staging.py install
python3 deploy/staging/staging.py schedule
```

The private signing key is protected on the workstation at
`~/.config/hello/staging/cd/signing-private.pem` and installed only as GitHub
staging environment secret `STAGING_CD_SIGNING_KEY`. The VM receives only
`cd/verification.pem`. Repeat setup refuses a different VM trust key or existing
environment policies that would need weakening. Rotate keys through a reviewed
procedure; do not overwrite trust files casually.

`STAGING_SCHEMA_VERSION=41` is an environment variable, not a migration command.
A schema change requires separately reviewed migration, grants and receiver
compatibility updates. Production is not configured by this workflow.

Trusted agent updates are explicit through `staging.py install`; fetched code
cannot replace the CD receiver itself. To pause CD, set `enabled` to `false` in
its protected VM `cd/config.json` and rerun scheduling if removing the cron job.
This does not pause the application's existing routine pilot. Use the separate
pilot-stop procedure if sending itself must stop.

## Checks and fixes included

```sh
python3 -m unittest discover -s deploy/staging -p '*_test.py'
```

Protocol tests reject PR/fork/non-main/incomplete/failed CI, missing or duplicate
gates, wrong keys, altered signatures/SHA/schema, superseded commits and repeated
deploys. The operational tests retain rollback/fencing recovery checks.

The existing hourly backup job previously used the same nonblocking lock as the
minutely health job: losing their simultaneous minute-07 race skipped an entire
backup hour. Backups now wait up to five minutes; health/CD polls may still skip
an already active invocation. A real-file-lock regression covers this race.

Existing hosted CI also exposed GO-2026-6859 in Go 1.26.10 and UTC/local timestamp
representation mismatches in replay fixtures. Pins move to Go 1.26.11 and its
verified official builder digest. The expected replay fixture and native outbox callback timestamps are
normalized to UTC while all complete-event, epoch, deadline, idempotency and
offset assertions remain unchanged. Vulnerability scanning is not suppressed.

References: [GitHub workflow-run security](https://docs.github.com/en/actions/reference/security/secure-use),
[environments without deployment records](https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/deploy-to-environment),
[Go security release](https://go.dev/doc/devel/release#go1.26.11), and
[GO-2026-6859](https://pkg.go.dev/vuln/GO-2026-6859).
