# AGENTS.md

Go notification-delivery backend (API server + Kafka worker + scheduler). Read this before editing code.

## Orientation facts that are easy to get wrong

- **The Go module is `hello`, not `notification-panel`.** Every internal import is
  `hello/internal/...`. The `notification-panel` name in `README.md` is only the upstream repo name.
- **`README.md` is stale in several places. Trust the code:**
  - it claims "no automated tests are configured" — 6 test files across 5 packages exist;
  - it points at `internal/config/config.go` — that file is split into `base|server|worker|scheduler|bootstrap.config.go`;
  - its project tree lists files that no longer match `git ls-files`.
- **Godoc here is unusually thorough and is the best map of the system** — it records known bugs,
  provider contract details, and concurrency constraints nowhere else. Read the comment on the
  symbol you are touching. But it is **only as fresh as the last fix**: where a finding was closed
  in code, the interface-level comment was often left behind (see *Known landmines*). When the
  comment and the implementation disagree, the implementation wins — verify by reading the body.
- **`code-review.md` is a live findings inventory** (C1–C29, H1–H26, …), not a closed document.
  Recent `main` commits are point-fixes against it. Before "fixing" something, check whether a
  finding already describes it; when you fix one, update `code-review.md`.

## Commands

```bash
go build ./...          # compiles clean on main
go test ./...           # RED on main — see below
go tool mockery         # regenerate mocks; NOT a global binary, and NOT in the Makefile
make sqlc               # regenerate internal/database/sqlc from db/{migrations,queries}
```

Focused verification of a single package:

```bash
go test ./internal/features/notifications/...
go test -run TestProcessor ./internal/features/notifications/...
```

**`go test ./...` fails on a clean checkout of `main`.** These are pre-existing, not your change:

| Package | Failure | Cause |
|---|---|---|
| `internal/features/staff` | build error | `service_test.go:488` calls `UpdatePassword` with 3 args; the service takes 4 (added a param) |
| `internal/features/segments` | build error | committed `mocks/mocks.go` is stale — missing `CountSearch` and `CountBySegmentIDs` |

`go tool mockery` fixes the segments *build* error, but the assertions then fail anyway: the
service now batch-counts members via `CountBySegmentIDs` (commit `8e26baf`, the N+1 fix) while
the tests still expect a per-segment `CountBySegment` call. Green packages:
`internal/token`, `internal/middleware`, `internal/features/notifications`.

**If you change a repository interface** consumed by a mocked package, regenerate
(`go tool mockery`) or the affected test package will not compile.

## Generated code and migrations

- `internal/database/sqlc/` (17 files) is **generated but committed**. `make sqlc` currently
  produces no diff. Never hand-edit it; change `db/queries/*.sql` or `db/migrations/*.sql`
  and regenerate, then commit both sides.
- `db/sqlc.yaml` reads `./migrations` as the schema. **A new migration must exist before you
  regenerate sqlc**, or sqlc cannot see the new columns/tables and will emit stale or failing code.
- Migrations are `golang-migrate` seq format, 6-digit prefix, `.up.sql` + `.down.sql` pairs
  (`make migrate-create NAME=...`). Add a **down** migration with every up migration.
- No CI, no linter, no formatter config. `gofmt` correctness is assumed, not enforced.

## Local environment gotchas

- **`Makefile:1` is `include .env`.** With no `.env`, *every* target fails — including
  `make help` and `make sqlc`. Copy `.env.example` first. Do not "fix" this by removing the include.
- **Kafka topics must be created by hand.** `kafka.NewProducer` sets
  `AllowAutoTopicCreation: false` and `RequiredAcks: RequireAll`, so a publish to a
  non-existent topic fails loudly. `docker-compose.yml` provisions no topics and there is no
  init script. Create both before the worker/scheduler do anything useful:
  `notification.send.requested` and `notification.delivery.updated` (names from `.env`).
- `make migrate-*` runs against `localhost:$(DB_PORT)` with `sslmode=disable` regardless of
  `DB_HOST`/`DB_SSLMODE` in `.env`. `.env` sets `DB_HOST=postgres` for the container network;
  that is expected and does not affect the migrate targets.
- `make up` runs three Air-hot-reload services (`server-dev`, `worker-dev`, `scheduler-dev`)
  from one `Dockerfile.dev`, selected by the `AIR_CONFIG` build arg. Air builds
  `./cmd/<name>/main.go` into `./tmp/` and excludes `_test.go`.
- `docker-compose.yml` does **not** pass `ONESIGNAL_APP_ID`/`ONESIGNAL_API_KEY` to `server-dev`
  or `scheduler-dev` — only `worker-dev` gets them. The worker is the only process that talks
  to the push provider.
- Local binaries that exist and are needed: `sqlc`, `migrate`, `docker`, `air`.
  `mockery` is deliberately absent — use `go tool mockery` (wired via the `tool` directive in `go.mod`).

## Architecture

Single binary set, one shared `*pgxpool.Pool` and one `sqlc.Queries` per process:

| Entry point | Role |
|---|---|
| `cmd/server` | REST API on `:8080`; composes all 7 feature modules; publishes `notification.send.requested` |
| `cmd/worker` | Kafka consumer, group `notification-worker-group`; OneSignal delivery; publishes `notification.delivery.updated` |
| `cmd/scheduler` | ticker (`SCHEDULER_INTERVAL`) promoting `SCHEDULED` → `QUEUED` |
| `cmd/bootstrap` | one-shot: seeds roles + first `SUPER_ADMIN` from `BOOTSTRAP_SUPER_ADMIN_*` |

Request flow: `routes.go` → `handler.go` → `service.go` → `internal/database/repository/` →
`internal/database/sqlc` → PostgreSQL. Services hold repository **interfaces** (declared in
each feature's `interfaces.go` for staff/segments) — that indirection is what makes mocks possible.
`pkg/response` is the only non-internal package and owns the JSON envelope.

Config is per-binary (`config.LoadServerConfig` etc.), never a global. Each loader calls
`godotenv.Load()` then **`log.Fatal`s on any missing required var** — there is no error return.
Config structs are read-once and must not be mutated after startup.

### Notification status state machine

Enforced by `CHECK` constraints in the migrations, and repeated as string literals all over the
Go code. A `CHECK` violation surfaces as a pgx error, not a Go type error.

- `notifications.status`: `DRAFT → SCHEDULED → QUEUED → PROCESSING → COMPLETED | FAILED`
  (plus `CANCELLED`)
- `notification_deliveries.status`: `PENDING → SENT → DELIVERED | OPENED`, or `FAILED`

Only `DRAFT` notifications are editable or deletable. `CANCELLED` appears in the constraint but
is never written by any Go code — treat adding a cancel path as new work, not a wiring fix.

## Known landmines (do not "simplify" these)

- **`notifications/provider.go`'s interface Godoc is STALE — do not trust it.** It still claims
  `OneSignalProvider` drops short tokens (returning a shorter slice), returns `nil, err` on batch
  failure, and hardcodes `click_action`; and that `FCMProvider` never returns a non-nil
  function-level error. All of that was fixed in the H19–H26 pass. The current `Send`
  implementations honour the contract: `OneSignalProvider.Send` pre-allocates
  `results := make([]SendResult, len(req.Tokens))` and marks rejected tokens `FAILED` **in place**,
  so the slice is always full-length. Read `onesignal_provider.go` / `fcm_provider.go` /
  `processor.go` — not the interface comment — when reasoning about provider behaviour.
- **Delivery attribution is `PushToken`-keyed, not positional.** `Processor.ProcessSendRequested`
  matches each `SendResult.PushToken` against the tokens slice it dispatched (finding H28's fix;
  the old code matched on `UserID` and broke at the first hit, collapsing every delivery for a
  multi-device user onto one device). It still *depends* on one result per input token in input
  order. On no match it inserts with `deviceTokenID = 0` and logs a warning — so a provider that
  returns results out of order or with rewritten tokens corrupts attribution silently rather than
  erroring.
- **`MockProvider` lives in the production package**, not behind a build tag or `_test.go`.
  It is not wired into any binary — the worker hardcodes `NewOneSignalProvider`
  (`cmd/worker/main.go`). Do not assume swapping providers is config-driven; it is a code edit.
- **The worker is at-least-once.** Offsets commit only after the processor returns nil, so
  `ProcessSendRequested` must stay idempotent. H27 is fixed: the guard treats `COMPLETED`/`FAILED`
  as terminal outright, and `PROCESSING` as terminal **only when at least one delivery row already
  exists**. `PROCESSING` with zero delivery rows is deliberately treated as a crashed predecessor
  and re-dispatched — do not "tighten" that into a blanket `PROCESSING` guard, which would strand
  notifications that crashed before their first delivery insert.
- **`internal/kafka/producer.go` has deterministic keys but NO current key-based partition
  affinity.** `NewProducer` uses `kafka.LeastBytes`; the pinned kafka-go v0.4.51 balancer
  routes by byte count, not key hash. Its Godoc claims otherwise and is stale (finding H47).
  Preserve the existing `notification_<id>` / `delivery_<id>` key formats. ADR-07 proposes
  an explicit hash balancer plus a reviewed mapping migration; this is not implemented yet.
- **`GetDatabaseURL` does not percent-encode** the password. DB passwords must stay alphanumeric.
- **Audit action strings are persisted schema.** They live in `internal/audit/constants.go`
  (`ACTION_*`, `ENTITY_TYPE_*`); changing a value breaks downstream log-search tooling.
  Register new actions there, not inline.
- **Role labels do not imply enforced permissions.** `VIEWER` is seeded as read-only,
  but current notification/segment mutation routes/services do not enforce that operation
  distinction (C21, open). Do not assume a JWT gate protects send/schedule/edit by role.
  ADR-11 proposes service/route permissions; this is not implemented yet.
- **500 responses must use the literal `"internal server error"`, never `err.Error()`.**
  `err.Error()` is correct only on mapped 4xx branches (401/403/404/409/400), where it surfaces a
  domain sentinel. The blanket 500 leak was fixed, with exactly three hold-outs left — all in
  `internal/features/segments/members_handler.go` (lines 108, 195, 258). Match the surrounding
  handler rather than copying those three.
- **Several list endpoints historically computed `Total` from an unfiltered `Count`, and some
  filters did not compose.** Fixes landed for users/staff/segments (search-aware `CountSearch`,
  batched `CountBySegmentIDs`). `members_service.go` is still the weakest area — check
  `code-review.md` before assuming any list endpoint is correct.

## Conventions

- Commit directly to `main`. History is linear; no PR/branch workflow, no CI.
- Conventional Commits with a scope: `fix(segments): ...`, `feat(audit): ...`,
  `perf(notifications): ...`, `docs(staff): ...`, `refactor(config): ...`.
- Feature modules follow a fixed file layout (`module.go`, `routes.go`, `handler.go`,
  `service.go`, `dto.go`, `model.go`, plus `interfaces.go` where mocking is needed). New
  features copy the nearest existing module rather than inventing a shape.
- Endpoints are documented in `docs/api/<module>-api.md`; schema rationale in `docs/database.md`.
  README's "Contributing Guidelines" requires updating the API doc alongside a new endpoint.
