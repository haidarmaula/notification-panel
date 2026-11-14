# Full local development environment

This runs the actual API, BFF, PostgreSQL, Kafka, admission worker, delivery
worker and scheduler, plus the Solid/Vite frontend. Only external OAuth/FCM is
simulated. It is independent of office Wi-Fi, VPN and staging. The repository
`.env` and staging credentials are never loaded by this runner.

## Start

For a fresh machine, use the canonical [machine setup guide](setup.md) to install
the required runtime tools. This `./dev` launcher requires Bash. Have Docker Desktop/Engine running
with Compose 2.24.4+, the Go toolchain in `go.mod`, Python 3, Node 22.12+ (the
project-tested release is 22.23.4), and pnpm 11.25.0 on `PATH`. The runner can
select an already-installed nvm Node 22 when the active Node is too old, but
nvm is optional: it does not install or change toolchains. Initial dependency
and image downloads require internet; cached resources can subsequently run
locally. No host PostgreSQL, Kafka or migrate binary is required. See the
[tool capability guide](tooling/development-tools.md) for which tools this
workflow needs.

Quick preflight from the repository root:

```bash
command -v bash go python3 node pnpm docker
go version
python3 --version
node --version
pnpm --version
docker compose version
docker info
```

If one is missing or has the wrong version, follow [machine setup](setup.md).
The runner needs Node 22.12 or newer; repository dashboard checks use the exact
CI-pinned Node and pnpm versions documented in the [tool guide](tooling/development-tools.md).

From the repository root:

```bash
./dev up
./dev credentials
```

Open **http://127.0.0.1:5173/login** and use the local email/password printed by
`credentials`. The initial email is `developer@hello.local`; the password is
generated once and retained locally. Staging accounts do not apply here.
Do not paste the password or private settings file into chat or commit them.

The first startup builds package binaries, generates synthetic TLS/account
material, creates private infrastructure, applies migrations, initializes the
application/grants and encrypted sample bindings, registers both Kafka sources,
and initializes admission offset 0 only once. Subsequent starts apply only new
migrations, preserve data/source IDs/consumer offsets, and refresh development
TLS material. Startup verifies the real login/application/audience path.

The sample workspace is **Hello Local**, with three synthetic Android users and
bindings. Campaign/template/segment/profile operations use the real database and
authorization. Sending is enabled only for the synthetic local environment.

## Develop, inspect and stop

```bash
./dev watch   # In another terminal: rebuild/restart after Go or migration changes
./dev check   # Read-only login, application, audience and process verification
./dev status
./dev logs
./dev down    # Stops this stack and host processes; retains all local data
```

Vite hot reloads frontend changes automatically after `up`. The optional watcher
rebuilds complete Go packages, migrates new SQL, and restarts the API/daemon/BFF
processes. A failed build leaves the previous binaries running. Ctrl+C ends the
watcher; `down` stops the services. A surviving watcher cannot resurrect a stack
after `down`. Infrastructure/configuration/script changes require `./dev up`.
BFF restarts expire browser sessions, so sign in again after a backend reload.

Applied migration checksums are retained. Editing an already-applied migration
fails rather than silently rewriting the schema: restore it and create a new
paired migration. The runner deliberately has no automatic data-wipe command.

Host listeners:

| Endpoint | Purpose |
| --- | --- |
| `http://127.0.0.1:5173` | Vite frontend and browser entry point |
| `http://127.0.0.1:5173/docs/` | OpenAPI reference with all three guides |
| `http://127.0.0.1:18090/bff/` | Local BFF; Vite proxies `/bff` here |
| `https://127.0.0.1:18082/api/v1/` | Actual API behind a verified mTLS development gateway |

The local BFF listens on port 18090, and Vite proxies `/bff` requests there.
The standalone Vite configuration defaults to BFF port 8090; the local runner
sets `HELLO_DASHBOARD_PROXY` to its own listener. This server-only routing
setting is not an API key. Published ports bind loopback only. If a port is
already occupied, stop its owner yourself; the runner never kills unrelated
processes.

Private generated material, host binaries, logs, screenshots and supervisor state
live under ignored `tmp/local-dev/`. PostgreSQL and Kafka data live in named
`hello-local` Docker volumes. `down` and `up` retain those volumes. Keep the private
settings directory together with the volumes: it contains the local login and
signing secrets. Do not delete it as a way to stop services.

Development certificates last seven days and rotate on `up`. The ordinary
integration fixture retains its six-hour certificate lifetime. If TLS fails after
leaving the stack running for a week, run `./dev up` rather than disabling TLS
validation. Source edits can build/reload without regenerating credentials.

## Local API reference

After `./dev up`, open <http://127.0.0.1:5173/docs/>. Choose **Staff & campaigns**,
**Applications & access**, or **Mobile & receipts**. The reference uses the
canonical contracts in `docs/contracts/`, with bundled Swagger UI assets and
no external validator. Reading the reference requires no credentials.

**Try it out** sends requests to this local origin's `/api/v1/` paths. Vite
forwards them to the local TLS gateway using the generated client certificate
and verified server CA. No API key or bearer token is supplied automatically;
dashboard login cookies do not authenticate these requests.

For staff API exploration:

1. Read the synthetic API key from `tmp/local-dev/config/api-key` on your machine
   and enter it in Swagger's **Authorize → ApiKey** field.
2. Execute `POST /api/v1/auth/login` using the email/password shown by
   `./dev credentials`.
3. Copy the returned access token into the bearer scheme shown by **Authorize**
   before calling protected staff/application operations. Choose an application
   returned by the local API when an operation requires an application UUID.

Mobile operations use their own mobile identity and proof contracts; staff login
does not issue a mobile credential. Keep synthetic credentials private. Swagger
holds authorization in page memory and clears it on reload or guide changes.
The API key remains server-side during ordinary dashboard use; entering it here
makes it available to the documentation page. Writes modify the persistent local
database, and sends use the local simulator.

`./dev up` rebuilds the reference under `tmp/local-dev/api-docs/` and verifies
that `/docs/` responds. Run it again after changing contracts or documentation
assets; `./dev watch` watches Go and migrations, not those files. Standalone
`pnpm dev` does not provision this reference: the local runner supplies the
artifact and TLS paths through server-only environment settings. Staging
publication follows its separate [API reference runbook](runbooks/staging-api-docs.md).

## End-to-end checks

The commands below use the tools already needed by the local runtime, plus the
dashboard's installed Playwright dependency. See the [dashboard guide](../web/dashboard/README.md)
for browser test setup and the [tool capability guide](tooling/development-tools.md)
for optional tools used by other workflows.

```bash
./dev smoke
./dev test-seed
pnpm --dir web/dashboard exec playwright install chromium
node scripts/local-dev/browser.mjs
```

`smoke` creates two local campaigns: immediate and scheduled. It verifies actual
provider acceptance through the BFF/API/Kafka/admission/preparation/delivery
pipeline; it leaves those synthetic campaigns available for inspection. It never
sends to a real phone. `test-seed` uses a separate temporary database inside this
private PostgreSQL instance, injects an initial seed failure, verifies rollback,
then retries without duplicate data; it removes only that test database.
The browser check signs in, visits seven actual local screens, signs out and
writes screenshots under `tmp/local-dev/screenshots/` without tracing credentials.

Fast orchestration/security checks without Docker:

```bash
python3 -B -m unittest discover -s scripts -p test_local_dev.py
go test -race scripts/integration-provider.go scripts/integration_provider_test.go
go test scripts/local-dev/setup.go
```

## Boundaries and limitations

The provider and delivery worker have only an internal Docker network. The
synthetic provider owns private DNS aliases for the fixed SDK Google hostnames;
it accepts only the three generated local destinations under an explicit local
mode. No real Firebase service-account file is mounted. The TLS ingress is the
only container attached to a host-facing network, and it accepts only local
client certificates. The API's internal development HTTP hop is not published.
See Docker's [network isolation](https://docs.docker.com/reference/compose-file/networks/)
and [loopback port publishing](https://docs.docker.com/engine/network/port-publishing/).

The local database roles, fixture signing/encryption keys and service account
are synthetic development identities, not a production hardening profile. Keep
them out of staging/production. Deployments continue to use their protected role
policies and independent credentials. No production loader protections are
weakened to make this environment work.

Simulator acceptance is not Android delivery or OPENED evidence. Real-device
enrollment/receipts and real FCM behavior require separate explicit setup and
authorization. The existing device-registration read API reads legacy device
rows; the sample pipeline uses modern encrypted bindings, so do not infer a
missing binding solely from an empty legacy device list. This environment does
not manufacture receipt events or automatically open messages.
