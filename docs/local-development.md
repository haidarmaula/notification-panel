# Full local development environment

The default `./dev up` runs the API, BFF, PostgreSQL, Kafka, admission worker,
delivery worker, scheduler, and React/Astryx/Vite frontend. It simulates external
OAuth and FCM and works independently of office Wi-Fi, VPN, and staging. The
runner keeps its settings separate from the repository `.env`.

Use the [real FCM profile](#real-fcm-on-an-android-device) to enroll an Android
test device and send a live push from a separate local stack.

## Start

For a fresh machine, use the canonical [machine setup guide](setup.md) to install
the required runtime tools. This `./dev` launcher requires Bash. Have Docker Desktop/Engine running
with Compose 2.24.4+, the Go toolchain in `go.mod`, Python 3, Node 22.13+ (the
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
The runner needs Node 22.13 or newer; repository dashboard checks use the exact
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

The three-guide reference is intentionally not exhaustive. The optional
[application onboarding API](api/application-onboarding-api.md) and its
[implemented OpenAPI contract](contracts/application-onboarding.openapi.json)
are linked from the [contracts index](contracts/README.md) but are not bundled
by `scripts/api_docs.py`. The local runner explicitly enables this protected
capability; ordinary server defaults and staging deployment policy do not.
Use the [synthetic setup tutorial below](#try-onboarding-in-the-dashboard), not
the broader [proposed contract](contracts/proposed/application-onboarding.openapi.json),
which does not describe the implemented route set. Local setup availability is
not authorization to enable staging endpoints or mutate shared environments.

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
./dev check-onboarding
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

`browser.mjs` signs in to the local dashboard, checks seven workspace screens,
saves campaign and settings screenshots under `tmp/local-dev/screenshots/`, and
signs out.

`check-onboarding` exercises the setup API through the real BFF using a dedicated
synthetic PROVISIONING application. It selects the local enrollment profile,
uploads the generated service account, checks immutable upload replay, waits for
the scoped OAuth verifier, and rejects cross-app operation lookup. The app stays
PROVISIONING with dispatch HELD; the command does not send a notification. Each
run retains a candidate version and verification evidence. The local developer
account is SUPER_ADMIN so it can exercise the same DB-backed management guard.
The demo keeps its verifier scope. A separate local fixture allocator also
assigns up to three new staging applications to fixed, separate verifier LOGINs.
Allocation starts after a candidate is stored; each scoped worker uses the real
control-plane executable. Scope assignments persist and are never reassigned to
another app automatically. This is a bounded synthetic development facility,
not the production runtime allocator. This check does not establish real Google
IAM, device delivery or production readiness.

### Try onboarding in the dashboard

1. Run `./dev up`, open `http://127.0.0.1:5173/login`, and use `./dev credentials`
   to find the local login.
2. Open **Application registry**, register an application, then select
   **Notification setup** on its detail page.
3. Choose **Hello Local synthetic Android**, enable Android, and enter
   `com.hello.local.onboarding` as its application ID. Save the profile.
4. Select **Staging** and upload the generated synthetic file
   `tmp/local-dev/tls/service-account.json`. Save the credentials.
5. Wait for **Verify FCM** to become available, then run it. The result should
   report OAuth success. The app stays **Provisioning** with dispatch **Held**.

This setup uses the generated synthetic service account with the local simulator.
The same screen also supports requesting enrollment activation, preparing and
queueing an approved device dry-run, and requesting PILOT or FULL dispatch
release. Those steps need separate operator actions and current readiness or
approval evidence as described in the [onboarding API guide](api/application-onboarding-api.md).
The tutorial ends at OAuth verification.

To reuse a verifier slot, open the `Uji onboarding dashboard` app created by the
browser check. Select **Change enrollment profile** to edit its saved profile.

For an automated real-browser check, run:

```bash
node scripts/local-dev/onboarding_browser.mjs
```

Use Node 22.13+ (for example `nvm use 22.23.4`). The check signs in, creates or
reuses one test app, saves its profile and synthetic FCM candidate through the
dashboard and BFF, and waits for the scoped OAuth verifier. It confirms the app
stays `PROVISIONING` with dispatch `HELD` and checks that credential material is
absent from rendered text and browser storage. It captures light desktop, light
mobile, and dark mobile screenshots under `tmp/local-dev/screenshots/`, then
signs out. The check sends no notifications and runs without request tracing.

Fast orchestration/security checks without Docker:

```bash
python3 -B -m unittest discover -s scripts -p test_local_dev.py
go test -race scripts/integration-provider.go scripts/integration_provider_test.go
go test scripts/local-dev/setup.go scripts/local-dev/setup_test.go
```

## Real FCM on an Android device

Use a Firebase test project with an Android app registered as
`com.example.hellofcmsmoke`. The Android client needs that project's
`google-services.json` in `hello-fcm-smoke/secrets/`. The backend needs a separate
service-account JSON authorized to send FCM messages to the test project.
Keep the service-account file private on the backend host. The worker uses the
host's public CA bundle to verify Google's HTTPS endpoints.

Start the profile from the backend repository root with the Firebase test
project ID and the absolute path to its service-account JSON.

```bash
export HELLO_LOCAL_FCM_PROJECT_ID='your-firebase-test-project'
export HELLO_LOCAL_FCM_SERVICE_ACCOUNT_FILE='/absolute/path/to/service-account.json'
chmod 600 "$HELLO_LOCAL_FCM_SERVICE_ACCOUNT_FILE"
./dev up --real-fcm
./dev credentials --real-fcm
```

Open the **Test Devices** link printed by `up` and sign in using the credentials
from the command above. A browser visit to `127.0.0.1:5273` redirects to the
canonical `localhost` origin. The two local profiles use separate DB and Kafka
data, application credentials, and browser sessions.

1. Keep the Mac and Android phones on the same private Wi-Fi. In **Test
   Devices**, build the Android development APK if prompted, then download and
   transfer it to each phone. The build uses the sibling `hello-fcm-smoke`
   checkout with Node, Java 17, and the Android SDK. Install the APK once per
   phone. Use **Rebuild APK** when the smoke app source changes, then install the
   new build over the existing APK. The APK
   opens directly to Hello FCM Smoke using its embedded JavaScript bundle and
   trusts the public CA from this Mac.
2. Click **Create pairing QR** and scan it from the installed app. Generate a
   fresh QR for each phone or to reconnect after the Mac's IP changes. Use the
   same APK for the same Mac CA. This flow uses neither ADB nor Metro.
3. In **Test Devices**, rename devices for easy identification and select one
   as the test target. Other phones stay enrolled. Use **Forget** only when
   removing an enrollment. Then click **Send test push** to create and dispatch
   a campaign through the normal backend and FCM path.
4. Check the latest test timeline for campaign and provider status and check the
   notification on the selected phone. Provider acceptance and device receipt
   are separate. Use staging for the HTTPS receipt-ingestion acceptance test.

**Public app variant.** The **Public app (domain TLS)** section builds a second
APK that embeds no CA at all: it trusts the Android system store only and
forbids cleartext. **Create public pairing QR** encodes the public API origin
(`HELLO_PUBLIC_API_ORIGIN`, default the placeholder
`https://api.hello-staging.example`, matching the staging gateway
`PUBLIC_HOST`). This variant is for the real public domain once infra delivers
a publicly trusted certificate; until then the phone will reject the
placeholder certificate. Enrollment against staging additionally requires the
issuer endpoints (`/v1/session`, `/v1/origin`) to be served at the public
origin, which is separate staging-side work.

The dashboard identifies Wi-Fi and APK problems under **Diagnostics**. On a
private network where automatic detection chooses the wrong interface, set
`HELLO_LOCAL_FCM_LAN_HOST` to the Mac's private IPv4 address before running
`./dev up --real-fcm`. The mobile HTTPS ingress uses port `8443`; the dashboard
and staff API stay on loopback. If port `8443` is busy, stop any terminal running
manual LAN pairing. Check `./dev status --real-fcm` for enrolled installations
and `./dev logs --real-fcm` for services. `./dev down --real-fcm` stops the
stack. On the next `up`, phones stay enrolled and dispatch starts on hold until
a target is selected again.

### Optional ADB and Metro workflow

For active development of the smoke app, connect an authorized Android device
over ADB, then forward the mobile API and issuer.

```bash
adb reverse tcp:8082 tcp:8082
adb reverse tcp:8181 tcp:8181

cd "$HOME/projects/hello-fcm-smoke"
nvm use
npm ci
HELLO_STAGING_BUILD=0 npx expo prebuild --platform android
HELLO_STAGING_BUILD=0 npx expo run:android --no-bundler
npm run start:local
```

Open the development build using the Metro QR. From the backend checkout, run
`./dev pair --real-fcm` and scan its pairing QR inside the smoke app. This
terminal also prints a manual code. The dashboard can select the test target.
For terminal-only use, pass the selected UUID from `./dev status --real-fcm`
to `./dev release-pilot --real-fcm --installation-id <uuid>`.

## Simulator boundaries and limitations

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
