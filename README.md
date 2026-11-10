# Notification Panel Backend

A Go backend for application-scoped notification campaigns, mobile installations, and durable push delivery.

> **Current status (2026-11-10):** The project is in pre-production acceptance. A local Android/FCM smoke is recorded, but production dispatch remains on hold. Local success is not staging or production approval; see the [readiness plan](docs/analysis/production-readiness-plan.md).

## How the system works

PostgreSQL is the durable source of truth. Kafka carries scoped work between processes. The API commits business state and an outbox row in PostgreSQL; the scheduler publishes that row to Kafka.

```text
Staff client
    │ app-scoped API request
    ▼
cmd/server ── transaction ──> PostgreSQL
                                  │ campaign state, idempotency,
                                  │ audit record, send outbox
                                  ▼
                            cmd/scheduler
                                  │ publishes send requests
                                  ▼
                                Kafka
                                  │
                       admission_only worker
                                  │ audience snapshot + delivery intents
                                  ▼
                              PostgreSQL
                                  │ durable delivery work
                                  ▼
                       delivery_only worker ──> FCM
                                  │ result ledger + delivery-update outbox
                                  └────────────> Kafka
```

1. The API checks current staff authority and application grant. Accepting a send commits the campaign state, idempotency record, audit entry, and send-outbox row together. A `202` means that transaction committed; it does not mean Kafka, FCM, or a device received the notification.
2. The scheduler promotes due schedules, publishes the send outbox, and recovers queued work and expired replay claims. It does not call a push provider.
3. An `admission_only` worker verifies each send event and prepares a durable audience snapshot and per-device delivery intents. It does not send pushes.
4. A `delivery_only` worker claims durable delivery work, submits through FCM using its protected route and credential policy, records the result, and publishes versioned delivery updates from an outbox.
5. Mobile receipts enter through a separate, policy-controlled API flow. Provider acceptance, device receipt, and user engagement are distinct events.

The scheduler's database-backed queued-recovery path can prepare work without a Kafka event. It shares the preparer with admission, so both paths converge on the same persisted state.

## Processes

| Entry point | Responsibility |
| --- | --- |
| [`cmd/server`](cmd/server/main.go) | HTTP API; registers authentication, application-scoped, staff, and profile routes. |
| [`cmd/scheduler`](cmd/scheduler/main.go) | Schedule promotion, send-outbox publication, queued recovery, and replay-claim cleanup. |
| [`cmd/worker`](cmd/worker/main.go) | One role per protected policy: `admission_only` or `delivery_only`. Run separate configured instances for both roles. |
| [`cmd/bootstrap`](cmd/bootstrap/main.go) | One-shot initialization of roles and the first super-admin. |

Worker and scheduler settings come from protected per-component deployment policy, not the local `.env` file. The current Compose stack does not start either process.

## Local development: API and dependencies

Prerequisites: Go 1.26.10, Docker Compose, Make, and the `migrate` CLI for database migrations.

The Makefile includes `.env` even for its targets, so create it before using `make`. Keep any existing `.env`; do not overwrite local credentials.

```bash
test -f .env || cp .env.example .env
# Edit .env: replace placeholder database, API/JWT, and bootstrap credentials.
make up
make migrate-up
make bootstrap
```

`make up` starts PostgreSQL, Kafka, Kafka UI, and the hot-reloading `server-dev` API on `localhost:8080`. Kafka UI is on `localhost:8090`. `make migrate-up` runs migrations from the host and needs `migrate` installed; `make bootstrap` seeds the initial role and super-admin in the local database. Use `make logs-server` to inspect API logs and `make down` to stop the stack.

This is **not** a full delivery environment: Compose does not start the scheduler or either worker role. The `.env.example` still contains OneSignal-era entries; they do not configure the current protected FCM worker. Do not treat the sample environment as a complete delivery deployment or use real credentials in local files.

## Read the code in this order

1. Start at [`cmd/server/main.go`](cmd/server/main.go) and its `serverRoutes` function to see which modules are actually registered.
2. Follow campaign routes in [`internal/features/applications/module.go`](internal/features/applications/module.go), then send acceptance in [`internal/features/applications/acceptance.go`](internal/features/applications/acceptance.go).
3. Trace scheduled work and outbox publication in [`internal/scheduler/runtime.go`](internal/scheduler/runtime.go).
4. See role selection and process wiring in [`internal/daemon/runtime.go`](internal/daemon/runtime.go); admission prepares snapshots in [`internal/dispatch/preparation.go`](internal/dispatch/preparation.go).
5. Follow delivery polling, FCM submission, result recording, and update publication in [`internal/dispatch/delivery_poller.go`](internal/dispatch/delivery_poller.go), [`internal/dispatch/submission.go`](internal/dispatch/submission.go), [`internal/dispatch/results.go`](internal/dispatch/results.go), and [`internal/daemon/delivery.go`](internal/daemon/delivery.go).
6. For persistence, read `db/migrations/` and `db/queries/`; `internal/database/sqlc/` is generated and must not be edited by hand.

> **Legacy boundary:** [`internal/features/notifications`](internal/features/notifications) remains in the repository, but the current server does not register that module. It and [`docs/database.md`](docs/database.md) describe parts of the earlier OneSignal-oriented design. For the current scoped send contract, start with the [campaign acceptance API](docs/api/campaign-acceptance-api.md), the code tour above, and the readiness plan. Treat migrations and implementation as authoritative when older documentation conflicts.

## Checks

From the repository root:

```bash
go test ./...
go build ./...
```

The CI script exposes focused gates and does not require `.env`:

```bash
bash scripts/ci.sh test
# Other gates: format, modules, build, vet, vuln
```

Unit tests and local smoke evidence do not establish production readiness. The readiness plan records open release gates and the scope of the [local Android FCM smoke](docs/analysis/local-fcm-smoke-handoff.md).

## Project guidance

- [Repository instructions](AGENTS.md)
- [Go standards](docs/go-standards.md)
- [Pinned Go skill provenance](docs/tooling/go-skills.md)
- [Current production readiness and evidence](docs/analysis/production-readiness-plan.md)
