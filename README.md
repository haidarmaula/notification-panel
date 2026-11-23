# Notification Panel Backend

A Go backend for push-notification campaigns across multiple mobile applications. Staff choose an audience and send or schedule a campaign; background processes turn it into device-level work, submit through Firebase Cloud Messaging (FCM), and track provider results and mobile receipts.

**The key idea:** accepting a campaign, sending it to FCM, and receiving it on a device are different events. PostgreSQL records durable progress; Kafka transports work and delivery updates.

This repository contains the backend and a staff dashboard under `web/dashboard`; production mobile applications are external clients. The Go module/import prefix is `hello`.

## Quick start

Setting up a new machine? Follow the [new machine setup guide](docs/setup.md) for
official tool installation links, version checks, and the local application
startup steps. It covers the complete local stack without staging or provider
credentials.

After completing setup, run this from the repository root:

```bash
./dev up
./dev credentials
./dev check
```

Open <http://127.0.0.1:5173/login>. The setup guide explains the generated local
account and the successful check output.
The local OpenAPI reference is available at <http://127.0.0.1:5173/docs/>;
see [local API documentation](docs/local-development.md#local-api-reference).

To send a real FCM test to Android, use the separate
[real-FCM profile](docs/local-development.md#real-fcm-on-an-android-device).
`./dev up --real-fcm` prints the **Test Devices** link. The dashboard supplies
the standalone test APK and pairing QR and lets you select one enrolled phone
for a test push.

## Start here

Read in this order; each page answers a different question.

| Step | Page | What you should understand afterwards |
| --- | --- | --- |
| 1 | [Domain model](docs/domain-model.md) | What application, campaign, installation, binding, delivery, and attempt mean. |
| 2 | [One notification, end to end](docs/notification-journey.md) | What happens after staff press Send, which data changes, and which code owns each step. |
| 3 | [Architecture and failure boundaries](docs/architecture.md) | Why processes are separate, why outboxes exist, and what retries do and do not guarantee. |
| 4 | [Developer guide](docs/development-guide.md) | How to run checks, start the local API, verify a response, and make a small first change. |

You do not need to read every ADR, migration, or runbook first. Return to the reference map below when a task requires more detail.

## The system in one picture

```text
Staff browser (React/Astryx) -> Go BFF -> API
                                         |
                                         v
                    PostgreSQL: accepted campaign + send outbox
                                         |
                      scheduler -> Kafka -> admission worker
                                                |
                                                v
                          PostgreSQL: audience + delivery intents
                                                |
                                         delivery worker -> FCM -> mobile app
                                                |                     |
                                      results + update outbox     receipt API
                                                |                     |
                                                v                     v
                                              Kafka              PostgreSQL
```

The BFF owns opaque browser sessions, checks Origin/CSRF on writes, and forwards only allowlisted routes. API keys and staff access/refresh tokens stay server-side; current API authorization and application grants still govern access.

The scheduler also recovers queued work directly from PostgreSQL using the same audience preparer as admission. Kafka consumption need not happen before that recovery path. The [walkthrough](docs/notification-journey.md) explains the state changes hidden by this small diagram.

- **Dashboard/BFF:** React/Astryx staff UI and Go browser session boundary in `cmd/dashboard`.
- **Server:** auth, application-scoped business, staff, and profile APIs; mobile APIs are policy-enabled separately.
- **Scheduler:** schedule promotion, send-outbox publication, queued recovery, and replay cleanup.
- **Admission worker:** validates send events and persists audience/delivery work; never calls FCM.
- **Delivery worker:** polls durable work, calls FCM, records results, and publishes delivery updates.

Admission and delivery are two protected modes of `cmd/worker`, not one consumer doing both jobs. Bootstrap, restricted requeue, and retention are additional initialization/operator commands; see the [process map](docs/architecture.md#runtime-map).

## First safe command

With the Go version declared in [go.mod](go.mod) available on PATH, from the repository root (see [toolchain preflight](docs/development-guide.md#toolchain-preflight) if `go` is not found):

```bash
go test -mod=readonly ./internal/jsonobject/...
```

This tests strict JSON decoding without Docker, a database, `.env`, or provider credentials. The first run may need network access to download Go tooling/modules. Continue with the [developer guide](docs/development-guide.md) for unit checks and the canonical full local environment in [full local development](docs/local-development.md).

For the canonical full local environment, run `./dev up` and open
`http://127.0.0.1:5173/login`. Run `./dev credentials` for the local account and
`./dev down` to stop without deleting data. The API, BFF, database, Kafka, workers
and scheduler are real; external FCM is an isolated simulator. See
[full local development](docs/local-development.md) for hot reload and checks.

## Where to go for a task

| Task | Start here |
| --- | --- |
| Change an HTTP endpoint or business rule | [Developer task map](docs/development-guide.md#4-where-to-make-a-change) |
| Change a frontend feature | [Dashboard guide](web/dashboard/README.md), [dashboard instructions](web/dashboard/AGENTS.md), [developer task map](docs/development-guide.md#4-where-to-make-a-change) |
| Change BFF sessions, login rotation or route allowlisting | [BFF implementation](internal/dashboard/server.go), [dashboard foundation](docs/specs/dashboard-foundation.md) |
| Configure application onboarding, FCM staging or verification | [Setup API](docs/api/application-onboarding-api.md), [runtime boundaries](docs/architecture.md#optional-onboarding-runtime), [local dashboard tutorial](docs/local-development.md#try-onboarding-in-the-dashboard) |
| Understand send/schedule semantics | [Campaign acceptance API](docs/api/campaign-acceptance-api.md) |
| Understand staff permissions and application grants | [Authorization API](docs/api/authorization-api.md), [application registry](docs/api/applications-api.md) |
| Integrate mobile installation and receipts | [Installation API](docs/api/mobile-installations-api.md), [receipt API](docs/api/mobile-receipts.md) |
| Change persistence | [Database documentation](docs/database.md), [queries](db/queries), [migrations](db/migrations); generated sqlc is not hand-edited |
| Configure or troubleshoot delivery | [Delivery-worker runbook](docs/runbooks/delivery-worker.md), [monitoring](docs/runbooks/monitoring.md) |
| Explore code and deployment boundaries | [Architecture](docs/architecture.md), [Floorplan source](floorplan/architecture.json) |
| Investigate restricted replay or maintenance | [Replay runbook](docs/runbooks/operator-replay.md), [retention runbook](docs/runbooks/retention.md) |
| Check release status | [Production readiness and evidence](docs/analysis/production-readiness-plan.md) |

## Current scope and documentation rules

The release plan keeps production dispatch on hold pending target acceptance.
Staging recorded one-device Android smoke and receipt results. Review the
[readiness plan](docs/analysis/production-readiness-plan.md) for the remaining
client, infrastructure, capacity, and recovery gates.

Retired global feature packages have been removed. Historical API contracts,
database references and ADRs remain for context; they do not establish current
runtime behavior. Scoped campaign routes live in
`internal/features/applications`. Start with [actual executable wiring](cmd/server/main.go)
and check current migrations and implementation when changing behavior.

For contributions, follow [repository instructions](AGENTS.md), [Go standards](docs/go-standards.md), and [pinned skill provenance](docs/tooling/go-skills.md). Keep relevant API contracts, onboarding explanations, and tests aligned when changing behavior. Never infer production readiness from a green unit suite.
