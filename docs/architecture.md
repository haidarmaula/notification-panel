# Architecture: responsibilities and failure boundaries

Read the [domain model](domain-model.md) first, then follow [one notification](notification-journey.md). This page explains why that journey crosses several processes, not how to deploy them.

## What this backend owns

The backend owns staff access to application-scoped campaign data, campaign acceptance and scheduling, audience preparation, durable delivery work, FCM submission records, and receipt projections. Mobile applications and their identity/enrollment integration are external clients. Firebase/FCM is an external delivery service; an accepted provider request is not proof that a device displayed a notification.

The server registers auth, applications, staff, and profile modules in [serverRoutes](../cmd/server/main.go). The applications module contains the scoped campaign, user, segment, template, and registry routes. Mobile installation and receipt routes are registered separately only when mobile policy is configured. Directory names alone do not tell you what the executable serves.

## Runtime map

```text
Staff browser (React/Astryx) -> Go BFF -> API
                                        |
                                        v
               PostgreSQL: campaign + acceptance + send outbox
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
| `cmd/dashboard` | Built React/Astryx assets and Go BFF; opaque browser sessions, Origin/CSRF checks, explicit upstream route allowlist | Expose API keys or staff tokens to the browser, replace API authorization, or send pushes |
| `cmd/server` | HTTP contracts, current staff/session/application authority, transactional business writes, optional mobile APIs | Call FCM as part of accepting a send |
| `cmd/scheduler` | Due schedules, send-outbox drain, queued recovery, expired replay-claim cleanup | Provider submission |
| `cmd/worker`, `admission_only` | Verify send source, persist audience/intents or quarantine evidence, then acknowledge consumption | Provider submission |
| `cmd/worker`, `delivery_only` | Poll one configured application's durable delivery work, call FCM, recover/finalize work, drain delivery updates | Consume the send topic as admission |
| `cmd/provisioner` | Optional root-owned credential staging over reserved, verified mTLS | Lifecycle changes, Docker deployment or dispatch release |
| `cmd/controlplane` | Optional app-scoped OAuth verification with fenced DB completion | FCM sends, automatic activation or pilot/full release |
| `cmd/bootstrap` | First super-admin account using the migration-seeded SUPER_ADMIN role | Seed roles or activate mobile delivery |
| `cmd/requeue` | Restricted operator reprocessing | Ordinary staff HTTP send workflow |
| `cmd/retention` | Policy-controlled maintenance | Unrestricted deletion or an automatic part of API startup |

Worker modes are selected by protected policy; they are separate configured instances of the same executable. Each database-using process owns its PostgreSQL pool; the BFF instead calls the API and holds bounded in-memory browser sessions. Campaign and delivery state are shared through the database, not shared Go memory. Start reading their composition in [daemon runtime](../internal/daemon/runtime.go), scheduler tasks in [scheduler runtime](../internal/scheduler/runtime.go), and delivery tasks in [delivery runtime](../internal/daemon/delivery.go).

### Browser session boundary

The React/Astryx UI calls `/bff/session`, `/bff/login` and allowlisted `/bff/api/...` routes, not the staff API with browser-held bearer tokens. The [BFF](../internal/dashboard/server.go) keeps API credentials server-side, rotates the opaque session ID and CSRF token at login, and requires the exact Origin and current CSRF token for writes. Upstream refresh is serialized per session after a definitive `401`; uncertain I/O is not a reason to replay a mutation. Sessions are single-replica, in-memory state; restarting the BFF requires login again. Backend database-backed authority and explicit grants remain mandatory. See the [dashboard foundation](specs/dashboard-foundation.md) for the session contract.

### Optional onboarding runtime

The optional [setup API](api/application-onboarding-api.md) connects enrollment
profiles, FCM credential staging and verification to a reviewed release
workflow. [`cmd/provisioner`](../cmd/provisioner/main.go) accepts reserved
credential stages over private mTLS. App-scoped
[`cmd/controlplane`](../cmd/controlplane/main.go) commands process verification,
prepare runtime reservations, and approve or apply lifecycle transitions under
separate database roles. Mobile clients enroll through the mobile API. The
controlled cohort dry-run records device-level outcomes before dispatch approval.
OAuth verification only confirms token issuance.

Runtime acknowledgements are scoped to a reviewed reservation and runtime
operation. The local fixture exercises the protected sequence; production
allocation, verifier provisioning, credential rotation, and recovery require
separate evidence. See the [runtime design](design/application-onboarding-runtime.md)
and [local setup tutorial](local-development.md#try-onboarding-in-the-dashboard).

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

The default [`./dev` environment](local-development.md) runs the React/Astryx
frontend, Go BFF, API, PostgreSQL, Kafka, workers, scheduler, and local onboarding
services with simulated OAuth and FCM. The separate
[`--real-fcm` profile](local-development.md#real-fcm-on-an-android-device) adds a
loopback Test Devices dashboard and a mobile-only HTTPS ingress on private Wi-Fi.
Test Devices enrolls multiple Android phones and selects one binding for real
FCM. Each test push uses the normal campaign and delivery pipeline.

[Staging](../deploy/staging/OPERATIONS.md) has its own deployment controller and
isolated test pilot. The [production readiness plan](analysis/production-readiness-plan.md)
tracks environment provisioning, mobile and consumer acceptance, capacity,
security, recovery, and operational release gates.

## How to interpret older design material

ADRs and [architecture diagrams](diagrams/README.md) preserve design decisions
and dated checkpoints. The [Floorplan source](../floorplan/architecture.json)
maps current code and environment boundaries. Use executable wiring and SQL
for runtime behavior, API contracts for request/response details, and the
readiness register for deployment evidence.
