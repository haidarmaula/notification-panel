# Deprecation and retirement audit

Audit date: 2026-11-14. Baseline: `9511e07` (`feat(dev): run complete persistent local notification stack`). This is a source and caller audit, not a completed cleanup or a production acceptance report.

The findings and recommendations below record the pre-cleanup baseline. The
dated implementation map records their disposition while retaining the
original evidence and limitations.

## Conclusion

The persistent local runner is additive: it did not retire the older Compose/Air workflow. Earlier application-scoping work retired global business entry points, but their packages, configuration and documentation remain. Cleanup must address both layers. An old file is not automatically deprecated; deployment evidence, migrations and active integration fixtures have different lifecycles.

## Implementation and closure — 2026-11-14

All 12 findings below have been addressed as code or documentation cleanup. This
does not certify production readiness, deployment acceptance, or an exhaustive
dependency deprecation scan. The baseline observations remain intact for audit
history; current behavior is represented by the implementation and maintained
contracts.

| Baseline finding | Disposition |
| --- | --- |
| P1: broken Air API build and competing local runtime | Removed the root Compose/Air runtime, `Dockerfile.dev`, all three Air configs, and their Make targets. `./dev` is the documented local runtime. |
| P1: active instructions described retired modules and OneSignal | Updated `AGENTS.md` and `docs/agent-reference.md` to follow current server composition, protected daemon paths, and scoped contracts. |
| P1: global API pages looked current | Marked the unmounted users, notifications, segments, and mobile contracts historical and linked current scoped/installation/receipt contracts. Retained the pages as historical references. |
| P2: Make reset/bootstrap/shell/log/build targets and tmux helper used the old stack | Removed the obsolete targets and `scripts/dev.sh`; retained migration and SQL generation targets and the `.env` include. Updated local development guidance. |
| P2: agent reference described Air services, manual topic setup, and obsolete host tools | Rewrote the local-environment and architecture guidance around `./dev`, current migration authoring, and protected runtimes. |
| P2: README and database overview were stale | Updated the maintained README and database overview. Historical schema descriptions remain labeled and retained for context. |
| P2: deprecated Firebase registration-token helper | Replaced `messaging.IsRegistrationTokenNotRegistered` with `messaging.IsUnregistered` in the integration fixture. |
| P2: deprecated `SendAll` use in an unmounted adapter | Retired the legacy notification feature and its FCM adapter; no unused `SendEach` migration was needed. The active `internal/fcm` path remains. |
| P2: obsolete environment-based worker and scheduler loaders | Removed the unused loaders and obsolete configuration path; updated the config fixture to stop setting their environment keys. The loaders had no dedicated tests. Protected daemon configuration remains. |
| P2: unused super-admin middleware and role setting | Removed the middleware, config value, and stale example/documentation setting. Current database-backed authorization remains. |
| P2: unmounted global feature graph and legacy Kafka producer | Removed the retired global feature packages and legacy Kafka producer/contracts, plus ten unreferenced repository wrappers after checking callers. The broker integration round trip now uses scoped send and delivery envelopes; the fixture retains key and payload assertions. Database queries, migrations, and generated SQL remain. |
| P3: onboarding navigation and generated Python caches | Added `docs/local-development.md` to the onboarding checker and ignored Python bytecode caches. The navigation check covers six pages. |

Boundaries from this audit remain in force: migration history, generated SQL,
Firebase, staging evidence, and integration/load/drill/security Compose fixtures
were retained. Validation completed on 2026-11-14:

- The race suite passed 1,372 tests across 43 packages; build and vet passed.
- Module verification passed, and `go mod tidy` produced no changes.
- `govulncheck` exited successfully with zero reachable vulnerabilities. It
  reported one imported-package advisory and three module advisories outside
  analyzed call paths.
- `./dev check` passed against the existing local stack.
- The isolated integration rerun exited 0: `hello/tests/integration` completed
  in 175.662 s and `internal/fcm` in 2.022 s. The simulator recorded 76 SDK HTTP
  requests and three OAuth requests; fixture cleanup succeeded. The first run
  exposed missing required targets in an exact-limit fixture. The fixture was
  corrected to use a BROADCAST target while retaining the expected 409 and
  no-mutation assertions; the rerun passed.
- 22 Python tests, seven provider-fixture race tests, and local-link checks for
  15 documents passed.

These checks do not establish production acceptance or an exhaustive dependency
deprecation scan.

## Confirmed findings

| Priority | Evidence | Finding and recommended action |
| --- | --- | --- |
| P1 | `.air.server.toml:8`; `Makefile:43–47` | The advertised Air API build compiles only `cmd/server/main.go`, omitting lifecycle definitions. The exact build fails with undefined `errHandlersBusy` and `serveHTTP`. Retire the old workflow in favor of `./dev`, or explicitly maintain and repair it. Leaving both advertised is misleading. |
| P1 | `AGENTS.md:25–27`; `docs/agent-reference.md:92–95` | Active engineering instructions still describe seven feature modules and OneSignal delivery. Current server composition is auth, applications, staff and profile; worker/scheduler delegate to protected daemon execution. Rewrite these instructions first so future maintenance follows actual wiring. |
| P1 | `docs/api/users-api.md:20`; `docs/api/{notifications,segments,mobile}-api.md` | Unmounted global APIs are presented as ordinary current APIs. The users page also claims body-supplied identity is accepted for simplicity, contradicting the corrected legacy implementation. Archive these contracts with explicit status and replacement links; update inbound references. |
| P2 | `Makefile:60–80`; `scripts/dev.sh:29` | Reset, bootstrap, shell, logs and build targets operate on old `server-dev` Compose resources, not the persistent `hello-local` stack. `build-all` builds only the server image. The tmux helper still tails the old stack. Remove or adapt these together with the old workflow, including help text. |
| P2 | `docs/agent-reference.md:67–83` | Claims about manually provisioned topics, three Air services, OneSignal credentials and required host Air/migrate binaries describe an older environment. Default Compose has only the API; `./dev` initializes its own topics and does not need host migrate. Replace global claims with workflow-specific requirements. |
| P2 | `README.md:7`; `docs/database.md:16–38` | README says the staff portal is absent although `web/dashboard` now exists. The database architecture diagram still ends at OneSignal while the active dispatch pipeline uses FCM, bindings, intents and attempts. Refresh maintained overviews; clearly delimit retained legacy schema documentation. |
| P2 | `tests/integration/dependencies_test.go:126` | Actual SDK deprecation: `messaging.IsRegistrationTokenNotRegistered` is marked deprecated by pinned Firebase v4.21.0 (`messaging.go:1054`). Replace with `messaging.IsUnregistered`; the old helper delegates directly to it. |
| P2 | `internal/features/notifications/fcm_provider.go:308` | Actual SDK deprecation: `SendAll` is deprecated in pinned Firebase v4.21.0 (`messaging_batch.go:274`). Already recorded as H25 in `code-review.md:341`. This adapter is unmounted; retire the legacy adapter with its feature rather than investing in an unused migration. If retained, `SendEach` changes request behavior and needs throughput validation. |
| P2 | `internal/config/worker.config.go:59`; `internal/config/scheduler.config.go:58` | Old environment-based worker/scheduler loaders have no callers. Current binaries use `daemon.Process` and protected policies. Remove loaders, dedicated tests and obsolete documentation as one change; preserve shared configuration still consumed elsewhere. |
| P2 | `internal/middleware/super_admin.go:56`; `internal/config/server.config.go:102` | The old super-admin middleware constructor has no callers; `ServerConfig.SuperAdminRole` has no runtime consumer. Retire this unused implementation/configuration after updating its tests and examples. Preserve current database-backed authorization. |
| P2 | `internal/features/{notifications,mobile,users,segments}`; `internal/kafka/producer.go:49` | These global feature packages have no handwritten non-test importing packages. The old Kafka producer's only caller is the unmounted notifications module. Remove this retired graph and its exclusive tests/mocks together; inspect repository and SQL consumers separately. |
| P3 | `scripts/check-onboarding.py:14–20`; `.gitignore` | Navigation checks cover five onboarding pages but exclude `docs/local-development.md`. Python bytecode caches also appear as untracked files under scripts and staging. Add the canonical local guide to navigation checks and ignore bytecode artifacts; caches are generated clutter, not deprecated functionality. |

## Boundaries that must survive cleanup

- Keep `compose.integration.yml`: `./dev` actively uses it as its base. The integration, delivery-load, Kafka drill and worker-security stacks serve distinct verification purposes; age alone does not justify deletion.
- Keep Firebase: active `internal/fcm` and `internal/pushpayload` still depend on it. Removing a legacy provider does not eliminate this dependency.
- Keep migration history and generated SQL contracts until their consumers and upgrade paths are accounted for. Never delete legacy tables solely because a feature package is unmounted. For example, the mounted applications API still exposes a device-list read (`internal/features/applications/module.go:36`).
- Keep staging runbooks, ADRs, specs and recorded evidence when they explain deployment constraints or decisions. Mark historical status and keep current navigation clear instead of presenting old instructions as current.
- Keep SQL generation and migration authoring capabilities. The new runner's automatic local migration does not replace authoring and deployment migration tools. Repository instructions currently require the Makefile `.env` include; removing that behavior requires an explicit policy change.
- Preserve current database-backed authorization, protected daemon policy, Kafka keys and delivery idempotency. Retiring unused role middleware must not alter these guards.
- Existing private settings and Docker volumes are user data. Removing old scripts does not authorize deleting either stack's volumes or credentials.

## Recommended cleanup sequence

1. Correct active agent instructions, README and architecture/database overviews; designate `./dev` as the canonical full local workflow. Archive unmounted API contracts and repair inbound links.
2. Retire the old Compose/Air runtime targets, configuration files and tmux assumptions together. Preserve useful authoring targets and update the developer guide, `.env.example`, help output and checks accordingly.
3. Replace the deprecated integration error helper. Remove the unmounted feature graph, legacy producer, unused middleware and loaders in focused changes, regenerating affected mocks through `go tool mockery` where needed.
4. Inspect residual repository/SQL/configuration consumers after package retirement. Only then remove exclusive helpers or dependencies and run module tidy. Plan schema retirement separately if persisted data must change.
5. Extend documentation checks to the canonical local guide, add bytecode ignores, and search for surviving references to retired commands, symbols and routes.

## Validation performed and limitations

- `python3 -B scripts/check-onboarding.py`: passed navigation, Bash syntax and safe-example checks for five pages. This checks structure, not semantic accuracy.
- `python3 -B -m unittest discover -s scripts -p test_local_dev.py`: all seven tests passed.
- `go build -mod=readonly -o /tmp/hello-audit-server-main ./cmd/server/main.go`: failed with the missing lifecycle symbols above, reproducing the old Air recipe defect.
- Compared current executable wiring, handwritten callers, recent commit history, existing review findings and the pinned Firebase SDK deprecation comments. No runtime code was changed.
- Did not start/stop containers, reset data, run real-provider delivery, run the full Go/CI suite, or audit every transitive dependency against upstream releases. No claim is made that these findings exhaust all possible deprecations. A clean removal requires the implementation and validation steps above.
