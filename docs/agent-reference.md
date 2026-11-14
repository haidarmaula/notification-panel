# Task-specific engineering reference

Moved from the root AGENTS.md on 2026-11-03 to load details only when relevant.
These remain project instructions. Read the sections required by root AGENTS.md
before changing the corresponding area; verify historical claims against code.

## Commands

```bash
go build ./...          # compiles clean on main
go test ./...           # unit tests; green baseline restored on 2026-11-02
go tool mockery         # regenerate mocks; NOT a global binary, and NOT in the Makefile
make sqlc               # regenerate internal/database/sqlc from db/{migrations,queries}
```

Focused verification starts with the package being changed. Examples:

```bash
go test ./internal/features/applications/...
go test ./internal/dispatch/...
```

Test packages and functions that exist in the current tree; retired global
feature packages are not test targets. Unit tests do not establish PostgreSQL,
Kafka, FCM integration or production readiness.

In restricted environments use `GOCACHE=/tmp/hello-go-review-cache` if the default
Go build cache is not writable.

Integration and delivery verification are opt-in and use isolated fixtures; see
the relevant current runbooks and `docs/local-development.md`. Do not treat a
legacy integration fixture as evidence for the current protected delivery path.

**If you change a repository interface** consumed by a mocked package, regenerate
(`go tool mockery`) or the affected test package will not compile.

## Generated code and migrations

- `internal/database/sqlc/` is **generated but committed**. `make sqlc` currently
  produces no diff. Never hand-edit it; change `db/queries/*.sql` or `db/migrations/*.sql`
  and regenerate, then commit both sides.
- `db/sqlc.yaml` reads `./migrations` as the schema. **A new migration must exist before you
  regenerate sqlc**, or sqlc cannot see the new columns/tables and will emit stale or failing code.
- Migrations are `golang-migrate` seq format, 6-digit prefix, `.up.sql` + `.down.sql` pairs
  (`make migrate-create NAME=...`). Add a **down** migration with every up migration.
- `.github/workflows/ci.yml` runs format/module/build/unit-test/vet and reachable
  vulnerability gates on main pushes, optional PRs, manual runs and weekly scans.
  Run the same checks via `bash scripts/ci.sh <format|modules|build|test|vet|vuln>`;
  no `.env` or Make target is needed. The workflow was validated locally on
  2026-11-02; a hosted Actions run/branch protection is not yet verified.
  Go version comes from go.mod; scanner is pinned in the script. Do not suppress
  vulnerability/network errors or infer production readiness from green checks.

## Local environment gotchas

`./dev` is the canonical full local environment; see
[`docs/local-development.md`](local-development.md) for prerequisites, setup,
commands, hot reload and verification. It starts the API, BFF, database, Kafka,
admission worker, delivery worker, scheduler and dashboard with simulated
external OAuth/FCM.

For fresh-machine installs and task-specific optional tools, see the [machine
setup guide](setup.md) and [tool capability guide](tooling/development-tools.md).
Plain Go unit/build work does not need Docker, `.env`, SQL migration tools,
Postman, Playwright, or provider credentials. `./dev` needs Bash, Docker with
Compose, Go, Python 3, Node and pnpm; it does not need host PostgreSQL, Kafka,
or migrate. The repository `.env` is used by Make targets and is separate from
the private settings created by `./dev`.

- **`Makefile:1` is `include .env`.** With no `.env`, *every* target fails — including
  `make help` and `make sqlc`. Copy `.env.example` first. Do not "fix" this by removing the include.
- The full local runner initializes its own persistent stack and synthetic
  application state. It does not load repository `.env` or staging credentials;
  follow its guide rather than manually creating local topics or credentials.
- For SQL migrations, use the runner workflow in the local guide for local
  development. Migration authoring and CI remain separate; preserve paired
  migrations and generated SQL requirements described above.
- `mockery` is deliberately absent as a global binary — use `go tool mockery`
  (wired via the `tool` directive in `go.mod`).

## Architecture

Each process owns a `*pgxpool.Pool` and `sqlc.Queries` instance:

| Entry point | Role |
|---|---|
| `cmd/server` | REST API; registers auth, applications, staff and profile; application-scoped routes are composed by applications; publishes send outbox records |
| `cmd/worker` | Protected daemon executable with admission and delivery modes; delivery submits through FCM and publishes delivery updates |
| `cmd/scheduler` | Protected daemon execution for due schedules, outbox publication, queued recovery and replay cleanup |
| `cmd/bootstrap` | one-shot: creates the first `SUPER_ADMIN` from `BOOTSTRAP_SUPER_ADMIN_*` using the migration-seeded role |

For current application routes, follow the module wiring into handler/service,
dispatch and repository code. Staff repositories declare interfaces in
`interfaces.go`. `pkg/response` owns the JSON envelope.

Config is per-binary, never a global. Local server/bootstrap loaders use
`godotenv.Load()` and terminate on missing required settings; protected worker
and scheduler loaders use deployment policy and do not read `.env`. Config
values are read once and must not be mutated after startup.

For current campaign lifecycle and delivery semantics, see the
[domain model](domain-model.md) and [notification journey](notification-journey.md).
Only DRAFT campaigns may be edited or deleted. `CANCELLED` is schema-only;
implementing cancellation is new work.

## Known landmines (do not "simplify" these)

- **Admission and provider delivery are separate protected worker modes.**
  Admission verifies send-source authority and persists audience/intents; the
  delivery mode claims durable work and submits through FCM. Preserve the
  policy checks and do not make API acceptance call the provider. See
  [architecture](architecture.md) and the delivery runbook.
- **Delivery is not exactly once.** Provider I/O can have an ambiguous outcome.
  Preserve attempt evidence, ownership leases, generation/attempt fences and
  idempotent result recording; do not retry provider calls just to repair a
  database acknowledgement.
- **The scheduler has two responsibilities:** publish send outbox work and
  recover eligible queued campaigns directly from PostgreSQL. Recovery uses
  the same audience preparer as admission; do not create a second preparation
  implementation.
- **Modern installation identity is binding-scoped.** Preserve immutable owner
  attribution and binding epochs when preparing audience or recording device
  attempts. Read the current domain model and relevant migrations before
  changing those relationships.
- **Audit action strings are persisted schema.** They live in `internal/audit/constants.go`
  (`ACTION_*`, `ENTITY_TYPE_*`); changing a value breaks downstream log-search tooling.
  Register new actions there, not inline.
- **Application permissions use current database authority and explicit grants.**
  Keep route and service/transaction guards; never substitute token RoleName
  checks or an allow-all test fallback.
- **500 responses must use the literal `"internal server error"`, never `err.Error()`.**
  `err.Error()` is appropriate only on explicitly mapped domain 4xx branches;
  unknown failures must retain the safe generic-error convention.
- Before fixing a tracked bug, search `code-review.md` for the finding and update
  its status when the issue is fixed. Validate current list filters and counts
  against implementation and tests; legacy API packages do not establish live
  behavior.
