# Full local development environment

This runs the actual API, BFF, PostgreSQL, Kafka, admission worker, delivery
worker and scheduler, plus the Solid/Vite frontend. Only external OAuth/FCM is
simulated. It is independent of office Wi-Fi, VPN and staging. The repository
`.env` and staging credentials are never loaded by this runner.

## Start

Prerequisites: Docker Desktop/Engine running with Compose 2.24.4+, the Go
toolchain in `go.mod`, Python 3, Node 22.12+ (tested with 22.23.4), and pnpm
11.25.0. Initial dependency/image downloads require internet; cached resources
can subsequently run locally. No host PostgreSQL, Kafka or migrate binary is
required. The runner selects an already-installed nvm Node 22 if the active
Node is too old; it never installs or changes global toolchains.

From the repository root:

```bash
nvm use 22.23.4
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
| `http://127.0.0.1:18090/bff/` | Local BFF; Vite proxies `/bff` here |
| `https://127.0.0.1:18082/api/v1/` | Actual API behind a verified mTLS development gateway |

The BFF uses port 18090 to avoid the older Compose Kafka UI on 8090. Vite's
`HELLO_DASHBOARD_PROXY` is a server-only development routing setting, not an API
key. The ordinary manual BFF recipe retains its default port 8090. Published
ports bind loopback only. If a port is already occupied, stop its owner yourself;
the runner never kills unrelated processes.

Private generated material, host binaries, logs, screenshots and supervisor state
live under ignored `tmp/local-dev/`. PostgreSQL and Kafka data live in named
`hello-local` Docker volumes. `down` and `up` retain those volumes. Keep the private
settings directory together with the volumes: it contains the local login and
signing secrets. Do not delete it as a way to stop services.

Development certificates last seven days and rotate on `up`. The ordinary
integration fixture retains its six-hour certificate lifetime. If TLS fails after
leaving the stack running for a week, run `./dev up` rather than disabling TLS
validation. Source edits can build/reload without regenerating credentials.

## End-to-end checks

```bash
./dev smoke
./dev test-seed
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
