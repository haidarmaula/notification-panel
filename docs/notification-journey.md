# One notification, end to end

Read the [domain model](domain-model.md) first. This is a worked explanation of the current pipeline, not a live-send tutorial. Do not run it against a real audience as an onboarding exercise.

## Our example

A staff administrator wants to send **"Maintenance tonight"** to one mobile user in application **Staff App**. That user has two eligible installations, one on each of two devices. We will call the new campaign **C** and its device delivery intents **D1** and **D2**; these are explanatory labels, not literal API identifiers.

Assume the application is active, the staff session and explicit project grant are valid, both installations have current bindings with approved `FCM_ACTIVE` routes, the audience coverage is approved, and runtime/dispatch policy permits work. These are real preconditions, not things that campaign creation silently sets up.

| Checkpoint | Campaign | Device work | What is established |
| --- | --- | --- | --- |
| Draft saved | `DRAFT` | None | Content and target specification exist. |
| Send accepted | `QUEUED` | Not necessarily prepared | Acceptance, idempotency, audit, and publication intent committed. |
| Audience prepared | `PROCESSING` | D1 and D2 `PENDING` | Eligible recipient/binding snapshot and work exist. |
| FCM accepts both | May await finalization | D1 and D2 `SENT` | Provider accepted the submissions, not proof of display. |
| Campaign finalized | `COMPLETED` in this example | Still `SENT` until observations | No unfinished work remains and the campaign has successful intents. |
| Devices report receipt/open | Campaign need not change | `DELIVERED` / `OPENED` as applicable | Authenticated client observations were recorded. |

## 1. Staff create the draft

The current campaign routes are registered in [applications/module.go](../internal/features/applications/module.go). `POST /api/v1/apps/{app_id}/campaigns` enters the handler and the application service, not the old unscoped notifications module.

[CreateCampaignDraft](../internal/features/applications/campaign_create.go) validates the scoped content/targets and writes the draft, its targets, and audit together. For our example, the target is an individual user. No provider request, delivery intent, or send event is created by merely saving this draft.

For request fields and validation, use the [draft API](api/scoped-campaign-drafts-api.md). Only `DRAFT` permits subsequent editing/deletion. The optional template association snapshots metadata; it is not a live pointer that rewrites campaign content later.

**Reading checkpoint:** find the transaction boundary and the query that inserts the target. Which failure would roll back the draft?

## 2. Staff press Send

The HTTP operation is:

```http
POST /api/v1/apps/{app_id}/campaigns/{campaign_id}/send
X-API-Key: <staff API key>
Authorization: Bearer <staff access token>
Idempotency-Key: <a stable key for this send>
```

It has an empty body. The braces and angle-bracket values above are placeholders; this is a contract sketch, not an executable curl command.

[AcceptCampaign](../internal/features/applications/acceptance.go) checks current authority and grant, campaign state, runtime/dispatch policy, and audience readiness. A new acceptance transaction commits:

- C's `QUEUED` state, acceptance timestamps, deadline, and lineage;
- the actor/application-scoped idempotency record;
- a row in the send outbox requesting later publication;
- the audit record.

The API returns `202` only after that transaction succeeds. An audit failure cannot leave a successfully accepted campaign behind. Nothing here promises broker publication or FCM acceptance.

If the response disappears, an authorized retry with the same retained idempotency key and campaign returns the original acceptance snapshot. That snapshot can still say `QUEUED` even if the current campaign is already completed. Read campaign history for current status; do not treat acceptance replay as a new send.

Contract: [campaign acceptance](api/campaign-acceptance-api.md). SQL: [durable acceptance](../db/queries/durable_acceptance.sql).

## 3. The scheduler hands off durable work

[Scheduler runtime](../internal/scheduler/runtime.go) runs four tasks: schedule promotion, send-outbox drain, queued recovery, and replay cleanup. For immediate C, publication reads the persisted send intent and publishes a scoped send event to the approved broker source. The API is not keeping an in-memory event alive on its behalf.

The [outbox](../internal/dispatch/outbox.go) removes a published row after acknowledged publication and database commit. A broker acknowledgement followed by an uncertain database outcome can result in publication again; downstream code must tolerate duplicates. An empty outbox means no pending publication row, not that a device received the message.

There is also a recovery path: the scheduler can find eligible queued campaigns in PostgreSQL and invoke the same preparer without waiting for a Kafka event. Publication and recovery run independently, so this walkthrough's ordering is explanatory, not a requirement that Kafka must win the race.

**If this were scheduled:** the API would first persist `SCHEDULED`. [Promotion](../internal/features/applications/schedule_promotion.go) queues it when due and inserts its outbox without extending the accepted deadline. Already-expired work fails rather than gaining a fresh lifetime on each restart.

## 4. Admission prepares the audience

The `admission_only` worker is wired in [daemon runtime](../internal/daemon/runtime.go). Its [consumer](../internal/dispatch/consumer.go) verifies the source and decodes the scoped event. [Prepare](../internal/dispatch/preparation.go) uses a database transaction to validate the accepted campaign and freeze audience/delivery work.

For our example, the one eligible user resolves to two current approved bindings, so preparation records the snapshot and creates D1 and D2, each retaining its original binding, owner, ownership epoch, and route identity. C becomes `PROCESSING`.

No provider call occurs during preparation. Incomplete coverage can record a hold instead of pretending a partial audience is complete. Repeating preparation uses the existing snapshot rather than silently expanding the campaign to a newly changed audience.

The consumer acknowledges only after a durable safe outcome. Invalid or exhausted messages require durable quarantine evidence; if that storage fails, acknowledgement is not safe. Scheduler recovery and Kafka admission converge on the same prepared state.

**Reading checkpoint:** why is a Kafka offset acknowledgement insufficient evidence that a push was sent?

## 5. Delivery makes authorized attempts

A separate `delivery_only` worker polls the database for its configured application. The [delivery poller](../internal/dispatch/delivery_poller.go) acquires/renews ownership, coordinates per-device work, and recovers or finalizes eligible campaigns.

For D1, [submission](../internal/dispatch/submission.go) obtains provider admission capacity, reserves a charged attempt, prepares the protected payload/address, and rechecks submission authority. The attempt records that a submission opportunity was charged, not necessarily that network I/O happened. Current ownership, route, lease, and deadline still matter.

In our happy path, FCM returns `accepted` for both devices. [RecordResult](../internal/dispatch/results.go) persists each exact attempt outcome and changes the device projection to `SENT`, enqueueing the versioned delivery update atomically. A transient database acknowledgement problem retries the result evidence, not that same provider call.

A later recovery may retry uncertain work under its policy. This is not exactly-once external delivery: a remote request may have succeeded even when the caller could not confirm it. Retry budgets and deadlines bound work; they do not eliminate every possible duplicate display.

## 6. Finalization and update publication are not receipts

[FinalizeCampaign](../internal/dispatch/finalize_campaign.go) evaluates remaining work and successful intents. In this example both sends succeeded, so C becomes `COMPLETED` after unfinished work clears. If one device failed and one succeeded, aggregate completion would still not imply all devices succeeded; inspect per-device records and statistics.

The delivery worker also drains the [delivery-update outbox](../internal/dispatch/update_outbox.go) to Kafka in a separate [runtime loop](../internal/daemon/delivery.go). Downstream consumers must reconcile event identity/version and duplicates. This is a status feed, not another send-request queue.

See the [downstream consumer contract](specs/m06-delivery-consumer.md) before implementing a consumer. A consumer outage must not be diagnosed by sending C again.

## 7. The mobile app reports what it observed

The [receipt endpoint](../internal/features/applications/mobile_receipt.go) is separately enabled by mobile policy. An authenticated client submits capability-authorized `DELIVERED` or `OPENED` evidence. [Observation recording](../internal/dispatch/observations.go) checks current ownership and deduplicates authorized observations. `OPENED` records a user read and implies observed delivery, but does not invent a separate `DELIVERED` fact/timestamp; an opened row can have null `delivered_at`. A receipt may arrive before provider-result persistence, in which case recording acceptance preserves the already observed `DELIVERED`/`OPENED` status instead of regressing it to `SENT`.

A successful FCM response alone never runs this step. Neither `SENT` nor campaign `COMPLETED` proves display or opening. Late valid observations may add facts while leaving a failed projection failed; consult the [receipt API](api/mobile-receipts.md), not a simplistic status ladder.

## Can you now explain the system?

Before reading more source, try answering:

1. What persisted after `202`, and what has not necessarily happened yet?
2. Why can one user produce two delivery intents?
3. Why can the scheduler prepare C before admission sees its Kafka record?
4. Why is replaying an idempotency key different from sending C again?
5. What prevents a stale installation owner or worker from redirecting old work?
6. Why can `COMPLETED` coexist with a failed device delivery or no device receipt?

If a term is unclear, return to the [domain model](domain-model.md). For the reasons behind the boundaries, continue to [architecture](architecture.md); for hands-on work, use the [developer guide](development-guide.md).
