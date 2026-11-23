# Hello staff dashboard

This is the canonical technical guide for the React 19/Vite browser application
using Astryx components and themes with a separate Go session BFF. See the
[capability map](../../DASHBOARD-CAPABILITIES.md) for product scope,
[foundation spec](../../docs/specs/dashboard-foundation.md) for decisions and
boundaries, and [design contract](DESIGN.md) for the visual contract. The original
SolidJS/Kobalte/Solid Query stack and green/neutral design direction are superseded,
not parallel implementation options. Use the installed Astryx guidance only for
UI work.
Staff administration, registry/grant management, scoped upload/audit reads, and
self-service password changes are described in the
[dashboard administration expansion](../../docs/specs/dashboard-administration.md).
The UI migration preserves the separate BFF and scoped backend contracts.

## UI architecture

All production pages use React hooks, React Router, React Query, and Astryx
components. Stone is the default design theme. The header theme segmented control and account
settings allow users to choose Stone, Y2K, or Matcha, independently
of Light/Dark/System appearance. Only explicit display preferences are persisted;
account data, tokens, private keys, and campaign content stay out of browser storage.

`src/astryx-themes.ts` is CLI-generated; use Astryx theme commands to update it.
Do not hand-edit it or eject a package theme unless an independent fork is needed.
The app root owns the Theme and LinkProvider integration. Route modules are loaded
on demand, and unsaved forms use the router blocker plus browser unload protection.

Theme fonts are self-hosted from Fontsource through `theme-fonts.config.ts` aliases.
This retains the packaged theme typography without external font requests or a
weaker Content Security Policy. No custom component CSS is used.

The separate `preview/` app remains available through `pnpm preview:astryx`.
It uses synthetic records; production routes always read the BFF.

## Local verification

Dashboard checks are optional for backend-only work. On a fresh machine, use
the [machine setup guide](../../docs/setup.md); dashboard checks use Node 22.23.4,
pnpm 11.25.0, Bash for selected project scripts, and the Go toolchain pinned in
the repository. These are the exact versions used by CI. Run from this directory:

```sh
pnpm install --frozen-lockfile
pnpm format:check
pnpm lint
pnpm test
pnpm build
pnpm budget
pnpm exec playwright install --with-deps chromium
pnpm test:e2e
pnpm exec astryx doctor --built src
```

`pnpm build` includes the strict TypeScript check. For BFF changes, also run
`go test -race ./internal/dashboard/...` from the repository root. The dated
[migration evidence](#astryx-migration-validation-2026-11-15) below is not a claim
that these checks have been rerun against the current tree.

The Playwright browser and its system dependencies are needed only for `test:e2e`.
See the [tool capability guide](../../docs/tooling/development-tools.md) for
install references and the distinction between dashboard, backend, and local
stack prerequisites.

Browser tests start a temporary verified HTTPS API fixture and the real Go BFF
on loopback ports 8091 and 4173. No staging/provider requests occur. Fixture
credentials are synthetic and exist only in `e2e/`. Tests cover real session
cookies and CSRF, draft editing, held acceptance, incomplete statistics and a
narrow viewport. They do not prove staging integration or device delivery.

For interactive fixture review after `pnpm build`, run `node e2e/server.mjs`
and open `http://127.0.0.1:4173/login`. Use `staff@example.invalid` and
`fixture-only`; this is exclusively the isolated fixture account. Stop the
process when finished. Send/schedule endpoints are held in the fixture.

## Real API development

For the entire actual local stack, run `./dev up` from the repository root and
open `http://127.0.0.1:5173/login`. Use `./dev credentials` for its generated
local account. See [full local development](../../docs/local-development.md).
This uses the real backend and persistent database, with only external FCM
simulated; `node e2e/server.mjs` remains an isolated frontend/BFF fixture.

The Go command requires `HELLO_DASHBOARD_CONFIG` to point at a private regular
JSON file (mode 0600). A local example, with operator-supplied paths:

```json
{
  "origin": "http://127.0.0.1:5173",
  "upstream": "https://10.1.16.109:8443",
  "api_key_file": "/absolute/private/path/api-key",
  "upstream_ca_file": "/absolute/path/staging-ca.crt",
  "listen_address": "127.0.0.1:8090",
  "assets_directory": "",
  "development": true,
  "max_sessions": 1000
}
```

The API key file is mode 0600 and contains the exact key with no trailing newline.
Optional `upstream_cert_file` and `upstream_key_file` provide a TLS client
identity; the key file is protected. Never put credentials in a Vite environment
variable. Run `go run ./cmd/dashboard` from the repository root, then `pnpm dev`
here. Development HTTP is permitted only for a loopback browser origin and
listener. Use the exact configured origin in the browser.

The browser calls `/bff/`; Vite proxies this prefix locally. The BFF forwards
only allowlisted operations to a fixed verified HTTPS API, with credentials
attached server-side. It never accepts a browser-supplied upstream host.

## Session and write semantics

Authenticated sessions are capped, remain in memory for at most twelve hours
and disappear on restart. Anonymous CSRF sessions have a separate bounded pool
and expire after five minutes. Cookies are HttpOnly and SameSite=Strict;
development-only cookies are insecure exclusively on loopback HTTP. Exact Origin
and session CSRF checks protect mutations. Refresh/logout are serialized per
session; an uncertain refresh invalidates the session without replay.

Queries are scoped to application IDs and kept only in memory. Logout clears
the browser cache. Ordinary pagination is not a cross-request snapshot. Unknown
draft-write outcomes disable immediate resubmission; inspect the campaign first.
Immediate sends reuse a deterministic key per actor/application/campaign via the
backend's scoped key semantics. A 202 is acceptance, never device delivery.

Canonical OpenAPI types are generated by `pnpm api:generate`. The browser client
targets the allowlisted BFF paths, never the authenticated backend directly.
With the full `./dev` stack, the separate OpenAPI reference is available at
`http://127.0.0.1:5173/docs/`. Its explicitly executed requests use the local
API proxy and Swagger credentials, independently of BFF sessions; see the
[local reference guide](../../docs/local-development.md#local-api-reference).
Charts and advanced table/form packages remain deferred until their concrete
screens need them. Administrative workflows are implemented locally; the
[administration spec](../../docs/specs/dashboard-administration.md#verification-status)
records their dated checks, not a current full-suite or target acceptance result.

## Optional application setup

Registry detail links to **Notification setup** at
`/apps/admin/applications/{app_id}/onboarding`. The screen reads readiness,
blockers, setup revision, operation status and safe approval metadata. It lets an
authorized administrator select a configured enrollment profile and platform
identifiers, upload an FCM service-account candidate, request OAuth verification,
poll its result, and hold dispatch. It also exposes lifecycle requests for
reviewed enrollment activation, a device dry-run, and PILOT or FULL release.
The screen shows cohort outcomes and review availability as they change.

This is an optional protected capability, not a consequence of creating an app
or building the dashboard. Default server configuration leaves the routes
unmounted. It requires protected onboarding policy, a credential provisioner,
approved profiles and database grants; OAuth verification also needs a
per-application verifier scope. Current backend `applications.manage` authority
remains required. Missing verifier scope is an unavailable capability, not a
successful check. The [onboarding API](../../docs/api/application-onboarding-api.md)
is authoritative for policy, routes and failure behavior; follow the
[local walkthrough](../../docs/local-development.md#try-onboarding-in-the-dashboard)
for synthetic setup rather than using production credentials.

The BFF exposes only the setup allowlist under
`/bff/api/admin/apps/{app_id}/onboarding`, retaining session/Origin/CSRF protection,
setup ETags and idempotency keys. Stale versions require reload and review;
uncertain outcomes allow an explicit retry of the same request, not a new write
with a fresh key. Uploaded credential material stays in component memory, not
browser storage or query caches, and is cleared after definitive upload success
or navigation.

Saving a profile or candidate leaves the app in provisioning. Distinct scoped
operator steps prepare and review runtime readiness and apply approved lifecycle
changes. Devices enroll through the mobile API. OAuth success confirms token
issuance; the local walkthrough uses the simulated provider.

## Android Test Devices

The local `./dev up --real-fcm` profile prints a direct link to **Test Devices**.
Use it to download the standalone APK, generate one-use pairing QRs, label
enrollments, and send through the campaign API to one selected installation.
The screen reports queued, provider-accepted, and observed-on-device evidence
separately. See the [real-FCM guide](../../docs/local-development.md#real-fcm-on-an-android-device)
for private network and APK requirements.

## Release boundaries

The protected staging deployment is implemented separately from development:
`--deployment` reads only the root-owned `/etc/hello/dashboard.json`, requires
mTLS ingress/diagnostics, pins upstream identity and applies verified-client login
rate limits. The runtime image is nonroot and readonly. Follow
[the staging dashboard runbook](../../docs/runbooks/staging-dashboard.md) for
journaled activation, CD, backup and rollback. Target acceptance must be checked
on the VM before reporting a deployment successful. Multi-replica/persistent
sessions require a separate design.

Production also requires real-browser TLS trust, UAT, accessibility/performance
validation on target devices, operational monitoring and the existing backend
production gates. Real notification sends need explicit recipient/count approval.
Target staging acceptance, UAT and production gates require separate evidence;
the local records below do not establish them.

## Astryx migration validation (2026-11-15)

This is historical migration evidence, not a fresh verification of the current
source tree or subsequent application setup changes. It records a full browser
run plus focused reruns, not one final full clean run.

The React/Stone migration passed strict TypeScript, ESLint without warnings,
Prettier, 11 unit tests, and the production build. All 50 browser cases were
verified against the isolated BFF fixture: the last full run passed 47 cases,
then the three corrected Astryx-specific assertions passed a focused rerun.
The final mobile table-width refinement also passed the two affected browser
cases. Browser coverage includes session/CSRF handling, draft editing, held and
uncertain acceptance, unsaved changes, scheduling, admin version conflicts,
credential handling, keyboard focus, mobile layout, and theme persistence.

Initial emitted JavaScript is 145.5 KiB gzip against the unchanged 160 KiB budget
(entry plus module-preload assets; feature routes load separately). The separate
preview app also builds. Astryx doctor reports zero failures and no source purity
findings. Its two font warnings inspect the original package stylesheets rather
than Vite aliases; browser tests verify local font loading without Google Fonts
requests. These checks do not establish real-provider delivery or deployment
readiness.

Production theme choices are limited to Stone (default), Y2K, and Matcha.
The separate preview retains all seven themes in `preview/astryx-themes.ts`.

The login photograph is bundled locally from `src/assets/autumn.jpg`.
