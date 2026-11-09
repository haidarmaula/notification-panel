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

Focused verification of a single package:

```bash
go test ./internal/features/notifications/...
go test -run TestProcessor ./internal/features/notifications/...
```

**The unit-test baseline was restored on 2026-11-02 (production plan step 17).**
Staff tests use authenticated actors, the four-argument UpdatePassword contract,
real bcrypt fixtures and pgx.ErrNoRows for missing rows. Segment mocks are
regenerated; list tests expect CountSearch and batched CountBySegmentIDs rather
than obsolete per-row counts. Tests cover authorization/password rejection and
repository failures without changing service behavior. The original five tested packages
(staff, segments, notifications, middleware, token) pass; M02a also adds authz unit tests. These are unit tests,
not PostgreSQL/Kafka/FCM integration or production-readiness evidence.

In restricted environments use `GOCACHE=/tmp/hello-go-review-cache` if the default
Go build cache is not writable.

Dependency integration smoke is opt-in: `bash scripts/integration.sh` with Docker
Desktop/daemon, Python 3, migrate and Postman CLI installed. It uses its own
Compose project, hardcoded local fixture DSN/ports, temporary storage and local
HTTP simulation; it does not load `.env` or start server/worker/scheduler. The
tagged tests refuse to run without explicit opt-in. See
`docs/analysis/integration-readiness.md`: repository/Kafka producer + Firebase SDK
Send are exercised, not the production FCMProvider SendAll adapter or new pipeline.

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
- `.github/workflows/ci.yml` runs format/module/build/unit-test/vet and reachable
  vulnerability gates on main pushes, optional PRs, manual runs and weekly scans.
  Run the same checks via `bash scripts/ci.sh <format|modules|build|test|vet|vuln>`;
  no `.env` or Make target is needed. The workflow was validated locally on
  2026-11-02; a hosted Actions run/branch protection is not yet verified.
  Go version comes from go.mod; scanner is pinned in the script. Do not suppress
  vulnerability/network errors or infer production readiness from green checks.

## Local environment gotchas

- **`Makefile:1` is `include .env`.** With no `.env`, *every* target fails — including
  `make help` and `make sqlc`. Copy `.env.example` first. Do not "fix" this by removing the include.
- **Kafka topics must be created by hand.** `kafka.NewProducer` sets
  `AllowAutoTopicCreation: false` and `RequiredAcks: RequireAll`, so a publish to a
  non-existent topic fails loudly. `docker-compose.yml` provisions no topics and there is no
  init script. Create both before the worker/scheduler do anything useful:
  `notification.send.requested` and `notification.delivery.updated` (names from `.env`).
  The separate integration runner provisions these names only on its disposable
  fixture broker; it does not initialize the development broker.
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
- **Notification/segment/member permissions use `internal/authz` current DB authority.**
  M02a fixed C21 with independent route/service guards, including non-null scheduling
  fields. Do not replace them with token RoleName checks or an allow-all test fallback.
  Legacy staff administration/session guards and transaction-time revocation/audit remain
  separate follow-up; authorization is currently a statement snapshot, not a lock through mutation.
- **500 responses must use the literal `"internal server error"`, never `err.Error()`.**
  `err.Error()` is correct only on mapped 4xx branches (401/403/404/409/400), where it surfaces a
  domain sentinel. M02a also fixed the three remaining members-handler unknown-500
  leaks; new handlers must keep the same safe generic-error convention.
- **Several list endpoints historically computed `Total` from an unfiltered `Count`, and some
  filters did not compose.** Fixes landed for users/staff/segments (search-aware `CountSearch`,
  batched `CountBySegmentIDs`). `members_service.go` is still the weakest area — check
  `code-review.md` before assuming any list endpoint is correct.
