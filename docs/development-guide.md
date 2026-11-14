# Developer guide

Read the [domain model](domain-model.md) and [notification journey](notification-journey.md) before changing delivery behavior. Run commands below from the repository root. These paths deliberately separate safe code exploration from environments that create database resources or contact providers.

## Toolchain preflight

Run `command -v go` and `go version` in the same shell you will use below. A Go installation outside PATH is not a missing installation: for the standard `/usr/local/go` layout, use `export PATH="/usr/local/go/bin:$PATH"` in that shell, then verify again. Other installation methods can use a different directory; do not replace an existing toolchain or change global shell settings just to run this guide.

The version must support [go.mod](../go.mod). These checks distinguish a shell setup problem from a failing build before you start infrastructure.

## 1. Start without infrastructure

Use the Go version in [go.mod](../go.mod), currently 1.26.11. Docker, `.env`, Firebase credentials, tmux, and a particular editor are not needed for ordinary unit tests.

```bash
go test -mod=readonly ./internal/jsonobject/...
go test -mod=readonly ./internal/features/auth/... -run '^TestAuthHandlersRequireJSONMediaType$' -count=1
go build -mod=readonly ./...
```

Expected: test packages report `ok`; a successful build is silent. The first run may download toolchain/modules, so this is credential-free, not necessarily offline. For a truly offline run, preinstall the required toolchain and cache modules; disabling proxies does not supply missing dependencies. If the Go cache is unwritable, set `GOCACHE=/tmp/hello-go-review-cache`.

Then broaden to `go test -mod=readonly ./...`. Opt-in integration tests are not activated by that command. Local CI gates are in [scripts/ci.sh](../scripts/ci.sh):

```bash
bash scripts/ci.sh format
bash scripts/ci.sh build
bash scripts/ci.sh test
bash scripts/ci.sh vet
```

The CI `test` gate includes race detection. `modules` downloads/verifies modules and checks for module-file drift; `vuln` installs a pinned scanner and consults vulnerability data. Those checks need network access. None requires `.env` or a Make target. Green unit/race checks are not real-provider or production evidence.

### Check these guides automatically

With Python 3 and Bash available:

```bash
python3 scripts/check-onboarding.py
python3 scripts/check-onboarding.py --run
python3 -B -m unittest discover -s scripts -p 'test_check_onboarding.py'
```

The first command checks the six onboarding pages' local links/headings, Bash syntax, and allowed safe examples. `--run` additionally executes the actual first safe Go block from this guide and the README, deduplicating repeated commands and failing on a nonzero exit or a test filter that runs no tests. It requires Go on PATH and may download dependencies. It never executes later database, API, integration, or provider examples. The last command tests the checker itself, including deliberately broken examples; these checks do not establish that the login walkthrough or live delivery works.

## 2. Run the complete local environment

Use [`./dev`](local-development.md) as the canonical local runtime. It starts
the persistent isolated frontend, BFF, API, PostgreSQL, Kafka, workers and
scheduler with a synthetic provider. The runner uses private local settings and
Docker volumes; it does not load the repository `.env` or staging credentials.
Follow its prerequisites, startup, login, checks and stop instructions before
using infrastructure-dependent examples.

The Makefile remains for migration authoring and SQL code generation. Its
targets include `.env`; see [`.env.example`](../.env.example) for the values
used by host migration commands. `make help` does not start the runtime.

### Why a new application cannot immediately send

Registry creation produces `PROVISIONING` without a staff grant. Even SUPER_ADMIN needs an explicit grant for project business data. Application activation, identity mappings, approved bindings/routes, runtime policy and coverage, broker source registration/checkpoints, and protected workers are separate setup. Bypassing those gates is not a shorter onboarding path. Follow the [application lifecycle](runbooks/application-lifecycle.md) and [delivery-worker](runbooks/delivery-worker.md) runbooks with the environment owner.

## 3. A small first change

Practice on the strict request boundary rather than real FCM sends:

1. Read [auth handler tests](../internal/features/auth/handler_test.go), especially `TestAuthHandlersRequireJSONMediaType`.
2. Add another table case next to the gzip case: valid `Content-Type: application/json` plus `Content-Encoding: br`.
3. Keep the existing expectation: HTTP 415, without reaching the nil service or leaking input values.
4. Run `go test -mod=readonly ./internal/features/auth/... -run '^TestAuthHandlersRequireJSONMediaType$' -count=1`.
5. Follow the handler's request validator to understand why rejection precedes the service call. Review your diff; keep or undo only your own exercise changes.

This is an optional learning exercise, not a missing product feature. It demonstrates route-to-handler boundaries, table-driven testing, and why infrastructure is unnecessary for input rejection.

## 4. Where to make a change

| Concern | Start with | Verify/update |
| --- | --- | --- |
| Which API is actually served | [server wiring](../cmd/server/main.go), [application route registration](../internal/features/applications/module.go) | Corresponding handler, API Markdown and contract in `docs/contracts/` |
| Scoped campaign draft | [campaign creation](../internal/features/applications/campaign_create.go), [draft mutations](../internal/features/applications/campaign_access.go) | Application package tests and draft contract |
| Segments and membership | [segment reads](../internal/features/applications/segment_reads.go), [segment writes](../internal/features/applications/segment_writes.go) | Scoped segment/member contracts and application tests, not only legacy segment tests |
| Templates | [template operations](../internal/features/applications/templates.go) | Scoped template contract and application tests |
| User/device reads | [scoped user reads](../internal/features/applications/user_reads.go) | Scoped user contract, application visibility and privacy tests |
| Campaign history, statistics, reads | [campaign history](../internal/features/applications/campaign_history.go), [statistics](../internal/features/applications/campaign_statistics.go) | History contract and receipt/provider semantics |
| Upload/audit reads | [upload and audit reads](../internal/features/applications/upload_audit_reads.go) | Scoped read contract; a read endpoint does not establish an upload-ingestion workflow |
| Send/schedule acceptance | [acceptance](../internal/features/applications/acceptance.go), [promotion](../internal/features/applications/schedule_promotion.go) | Authority, audit rollback, idempotency, deadline tests |
| Who receives a campaign | [preparation](../internal/dispatch/preparation.go), [preparation SQL](../db/queries/preparation.sql) | Coverage, deduplication, snapshot, replay tests |
| Provider request/results/retries | [submission](../internal/dispatch/submission.go), [results](../internal/dispatch/results.go), [FCM package](../internal/fcm) | Authorization fences, uncertainty, retry-budget tests; never use live sends as unit tests |
| Device ownership or receipts | [installation lifecycle](../internal/installation/lifecycle.go), [observations](../internal/dispatch/observations.go) | Account-switch, stale-proof, epoch, duplicate observation tests |
| Staff authority and mutation | [authz](../internal/authz), [staffmutation](../internal/staffmutation) | Current session/grant checks and transaction rollback |
| Storage | `db/queries/`, paired `db/migrations/`, then repository/generated callers | Generate sqlc; do not hand-edit `internal/database/sqlc/` |
| Runtime/configuration | [server config](../internal/config), [daemon composition](../internal/daemon/runtime.go), [deployment](../internal/deployment) | Correct role-specific policy and startup/shutdown tests |

Typical HTTP path: registered route → handler → service/domain operation → repository or transaction-bound queries → PostgreSQL. Not every feature has identically named files or a repository wrapper; follow the called function rather than assuming the old module template applies everywhere.

Start with `go test ./internal/features/applications/...` for application changes or the relevant package/`-run` filter. Broaden for shared changes. Repository interfaces consumed by generated mocks require `go tool mockery`, not a global executable. Schema changes require a six-digit up/down migration pair before `make sqlc`; keep generated changes with their source SQL. See [Go standards](go-standards.md) and [repository instructions](../AGENTS.md).

## 5. Exercise infrastructure in isolation

Only after the previous steps, inspect the fixture prerequisites and resource ownership before running:

```bash
bash scripts/integration.sh
```

This is an opt-in runner requiring Docker, Go, Python 3, migrate, and Postman CLI. It reserves local fixture ports (15432, 19092, 19095, 14010), builds/exercises the real delivery worker against a provider simulator, and removes its owned disposable resources. It does not load development `.env` or prove real Firebase/APNs/device behavior. The current runner is broader than the historical description "repository smoke only"; see [integration evidence and limits](analysis/integration-readiness.md).

For a small isolated load/pipeline exercise, the existing harness supports:

```bash
bash scripts/delivery-load.sh 32 1 healthy burst 400 2 100
```

Read [capacity evidence and harness limits](analysis/delivery-load.md) first. This requires local Unix-socket Docker, Go, Python 3, compatible Compose, and fixture images; it provisions disposable infrastructure with a synthetic provider. The healthy case checks 32 sent deliveries, 32 broker updates, drained update outbox, and distinct delivery identities. It exercises acceptance/outbox/preparation through fixture code and the real delivery worker, not a complete public-HTTP/scheduler launch or real-device delivery.

Neither fixture needs real FCM credentials. Do not substitute a live token or production database into a simulation. For real-device work, use the approved staging runbook; do not replay campaigns recorded in smoke handoffs.

## Reference boundaries

Protected server/bootstrap deployment uses `--deployment`; worker/scheduler use fixed protected policies. Restricted replay and retention require their own authority and review. Do not change file ownership/permissions or execute grant scripts just to make a tutorial succeed. There is no production onboarding shortcut in `.env.example`.

The current working tree may contain deployment/migration work under review. A local file's presence does not prove that its grants are installed or reviewed for the target environment. Migration tools apply pending files too: inspect the tree and coordinate ownership before migrating a shared database.

Next: consult the [architecture failure table](architecture.md#failure-questions-to-ask-while-reading) when diagnosing an issue, and the [readiness plan](analysis/production-readiness-plan.md) when deciding what has actually been verified in a deployment.
