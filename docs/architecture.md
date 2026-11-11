# Architecture: responsibilities and failure boundaries

Read the [domain model](domain-model.md) first, then follow [one notification](notification-journey.md). This page explains why that journey crosses several processes, not how to deploy them.

## What this backend owns

The backend owns staff access to application-scoped campaign data, campaign acceptance and scheduling, audience preparation, durable delivery work, FCM submission records, and receipt projections. Mobile applications and their identity/enrollment integration are external clients. Firebase/FCM is an external delivery service; an accepted provider request is not proof that a device displayed a notification.

The server registers auth, applications, staff, and profile modules in [serverRoutes](../cmd/server/main.go). The applications module contains the scoped campaign, user, segment, template, and registry routes. Mobile installation and receipt routes are registered separately only when mobile policy is configured. Directory names alone do not tell you what the executable serves.

## Runtime map

```text
Staff client -> API -> PostgreSQL: campaign + acceptance + send outbox
                                  |
                         scheduler publishes outbox
                                  v
                                Kafka -> admission worker
                                             |
                                             v
PostgreSQL: audience snapshot + per-device delivery intents
       ^                 |
       |                 v
       +--------- delivery worker -> FCM -> mobile app
       |                 |
       |                 +-> delivery-update outbox -> Kafka -> external consumer
       |
       +--------- receipt API <- mobile app

Scheduler queued recovery also prepares eligible campaigns directly from PostgreSQL.
It uses the same preparer as Kafka admission; this is not a second delivery pipeline.
```

The diagram shows logical handoffs, not mandatory machines or a single sequential loop. In particular, the scheduler's publication and queued-recovery tasks run independently. Preparation may complete before the corresponding Kafka event is consumed.

| Executable | Owns | Does not do |
| --- | --- | --- |
| `cmd/server` | HTTP contracts, current staff/session/application authority, transactional business writes, optional mobile APIs | Call FCM as part of accepting a send |
| `cmd/scheduler` | Due schedules, send-outbox drain, queued recovery, expired replay-claim cleanup | Provider submission |
| `cmd/worker`, `admission_only` | Verify send source, persist audience/intents or quarantine evidence, then acknowledge consumption | Provider submission |
| `cmd/worker`, `delivery_only` | Poll one configured application's durable delivery work, call FCM, recover/finalize work, drain delivery updates | Consume the send topic as admission |
| `cmd/bootstrap` | First super-admin account using the migration-seeded SUPER_ADMIN role | Seed roles or activate mobile delivery |
| `cmd/requeue` | Restricted operator reprocessing | Ordinary staff HTTP send workflow |
| `cmd/retention` | Policy-controlled maintenance | Unrestricted deletion or an automatic part of API startup |

Worker modes are selected by protected policy; they are separate configured instances of the same executable. Each process opens its own PostgreSQL pool; campaign and delivery state are shared through the database, not shared Go memory. Start reading their composition in [daemon runtime](../internal/daemon/runtime.go), scheduler tasks in [scheduler runtime](../internal/scheduler/runtime.go), and delivery tasks in [delivery runtime](../internal/daemon/delivery.go).

## Why these boundaries exist

### PostgreSQL owns business truth; Kafka transports work

Accepting a campaign must not depend on two independent writes succeeding at once. The API commits its business change and a publication intent, called an **outbox row**, in one database transaction. The scheduler later publishes that intent to Kafka. If the broker is unavailable, the accepted campaign does not revert to a draft merely because publication is delayed.

There are two outboxes: send requests, drained by the scheduler, and versioned delivery updates, drained by the delivery worker. They serve different consumers. A delivery update is a projection of a result, not an instruction to send another push.

Sources: [acceptance](../internal/features/applications/acceptance.go), [send outbox](../internal/dispatch/outbox.go), [update outbox](../internal/dispatch/update_outbox.go).

### Preparation and provider execution are separate

Preparation resolves the audience and persists work without provider I/O. Delivery execution later claims that persisted work. This separates "who should receive this campaign?" from "which authorized device attempt may run now?" and lets recovery use database state instead of an in-memory queue left inside a crashed process.

Sources: [preparation](../internal/dispatch/preparation.go), [delivery poller](../internal/dispatch/delivery_poller.go).

### Locks, leases, and fences solve different problems

A transaction lock protects a database mutation while it runs. A lease gives a worker temporary ownership across operations. A fence checks that the worker still owns the exact campaign generation and attempt before allowing a result to change state. An old worker must not overwrite a successor merely because its slow provider request eventually returned.

These mechanisms do not make a remote FCM request part of a PostgreSQL transaction. Provider I/O can have an ambiguous outcome. Persisting the original attempt evidence is different from making a second send.

Sources: [execution](../internal/dispatch/execution.go), [submission](../internal/dispatch/submission.go), [result recording](../internal/dispatch/results.go).

### Identity is not permission

A staff JWT identifies a session; current database authority and an explicit application grant govern business access. SUPER_ADMIN is not an automatic grant to every application's business data. Mobile installation credentials and receipt capabilities are a different trust boundary, not staff JWTs reused on a mobile device.

Sources: [application authorization](../internal/authz/application.go), [staff mutation](../internal/staffmutation/transaction.go), [mobile lifecycle](../internal/features/applications/mobile_lifecycle.go), [receipt handler](../internal/features/applications/mobile_receipt.go).

## Failure questions to ask while reading

| Failure | Where to look / what not to assume |
| --- | --- |
| HTTP response is lost after acceptance | Retry with the same authorized idempotency key; inspect acceptance rather than create another campaign. |
| Kafka publication is delayed | Inspect the send outbox, scheduler, source approval, and runtime holds. `202` was not a broker acknowledgement. |
| Same send event appears again | Admission/preparation reconciles persisted campaign and snapshot state. Do not assume every Kafka record creates new device work. |
| Admission cannot safely process an event | Consumer persists quarantine evidence before acknowledgement. Storage failure is not safe acknowledgement. |
| Worker loses ownership | Check lease/generation/attempt fences; do not bypass them to force a result through. |
| Provider outcome is uncertain | Do not promise exactly-once device delivery or immediately repeat provider I/O to repair a database acknowledgement. |
| Kafka delivery-update destination is unavailable | Delivery ledger work and update publication have separate loops; delayed publication is not proof of a failed provider send. |
| API works but mobile routes are missing | Check mobile policy registration, not only the public listener. |

For acknowledgement mechanics see [admission consumer](../internal/dispatch/consumer.go) and [update outbox](../internal/dispatch/update_outbox.go). Operator procedures belong in the [delivery worker](runbooks/delivery-worker.md), [replay](runbooks/operator-replay.md), and [monitoring](runbooks/monitoring.md) runbooks, not ad hoc SQL fixes.

## Configuration and local-versus-deployed behavior

Local server/bootstrap commands can load environment configuration. Protected deployment has separate configuration loaders. Worker and scheduler commands require protected per-component policy; copying `.env.example` does not activate them. Their startup verifies database and approved broker/source identity before starting work.

The default Compose stack is a development API plus dependencies, not a production topology. Production access, source registration, credentials, grants, dispatch policy, and acceptance evidence remain separate operator responsibilities. See [development guide](development-guide.md) for the safe starting point and [production readiness](analysis/production-readiness-plan.md) for current gates.

## How to interpret older design material

ADRs preserve decisions and proposals, including descriptions of older implementations. A proposed ADR is not evidence that a feature is absent or deployed. The [target diagram](diagrams/README.md) is a dated design snapshot. Use executable wiring and current SQL for implemented behavior, API contracts for request/response details, and the readiness register for deployment evidence.
