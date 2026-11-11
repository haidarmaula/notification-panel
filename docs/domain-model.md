# Domain model: the words behind the code

Start here before following [one notification](notification-journey.md). This page describes the current scoped pipeline, not the older OneSignal notification package.

## Two kinds of people, one application boundary

An **application** (also called a project in policies and contracts) is the business isolation boundary: for example, a staff mobile app and a customer mobile app are different applications even if this backend serves both. Public APIs use the application's UUID, not its internal numeric database ID.

A **staff user** operates the administration API. A **mobile user** is a potential recipient. These are different identities stored for different purposes; a staff account is not automatically a recipient account.

A staff role answers "what operations may this person perform?" An application grant answers "which application's business data may they access?" Both are needed. SUPER_ADMIN can manage the registry, but is not automatically granted every project's campaigns. A mobile user is resolved through application-scoped external identity mappings, not by trusting an arbitrary user ID supplied by a device. The recipient and identity mapping must already be provisioned; installation enrollment resolves that identity rather than creating an arbitrary new recipient.

```text
Staff user -- global role --> permitted operations
     |
     +-- explicit grant --> Application
                                |
                                +-- mobile user identities
                                +-- segments / templates / campaigns
                                +-- installations / bindings / delivery work
```

Creating an application leaves it `PROVISIONING` and grants nobody access. Activation and dispatch approval are separate operator-controlled operations; creating registry metadata is not permission to send real pushes.

Sources: [registry service](../internal/features/applications/service.go), [application authorizer](../internal/authz/application.go), [mobile authority](../internal/installation/authority.go), [registry contract](api/applications-api.md), [lifecycle runbook](runbooks/application-lifecycle.md).

## Campaign and audience

A **campaign** is the staff-authored notification: title, body, payload, target specification, and lifecycle. APIs call it a campaign; database and dispatch code also use **notification** for that same campaign identity. A campaign is not a single device push.

A **template** is reusable content metadata. Creating a campaign with a template stores a template-name snapshot alongside explicit campaign content; do not assume later template edits rewrite existing campaigns.

A **target** describes whom to include:

- `BROADCAST` selects the application's audience and must stand alone.
- `SEGMENT` selects members of an application-scoped group.
- `INDIVIDUAL` selects an individual mobile user.
- Segment and individual targets can be combined; user resolution unions and deduplicates them.

An **audience snapshot** is the persisted result of preparation, rather than a query recomputed before each provider call. It records resolved/eligible user and device counts. Preparation checks current device coverage and approved routes; incomplete coverage can hold the campaign instead of silently sending to a partial audience.

Example: one targeted user with two eligible app installations can produce two delivery intents. Two overlapping segments containing that user do not create a second copy of the same user's audience membership.

Sources: [campaign creation](../internal/features/applications/campaign_create.go), [draft mutations](../internal/features/applications/campaign_access.go), [preparation SQL](../db/queries/preparation.sql).

## User, installation, and binding are not interchangeable

| Concept | Plain-language meaning | Why it is separate |
| --- | --- | --- |
| Mobile user | Recipient account in the application | One person/account can use multiple devices. |
| Installation | Durable identity of an app instance | The app instance can refresh its address, log out, or switch accounts. |
| Binding | An immutable generation of an encrypted provider address and its routing identity | Provider tokens can rotate; old work must not silently redirect to a new token or owner. |
| Provider route | Approved application/provider namespace, route, platform, and address-type relationship | Possessing a token alone is not permission to submit through a Firebase project. |
| Owner epoch | A counter identifying the current ownership generation of the installation | Work accepted for the previous signed-in user must not become work for the next user. |

There is at most one active binding per installation. An address refresh can replace a binding without changing the owner epoch. Logout and account switch advance the epoch; they are ownership changes, not just token edits. Version checks and proofs protect lifecycle updates from stale requests.

Sources: [installation lifecycle](../internal/installation/lifecycle.go), [binding schema](../db/migrations/000018_create_installation_bindings.up.sql), [installation API](api/mobile-installations-api.md).

## Delivery intent versus attempt versus receipt

A **delivery intent** is a durable record saying "this campaign has work for this binding and original owner." It is created during preparation. It is not proof that the provider has been contacted.

An **attempt** is a charged, individually tracked reservation to submit that delivery work. A delivery can have more than one attempt within its deadline and retry budget. A reservation may fail before network submission, so an attempt count is not a count of notifications displayed.

A **provider result** records what the FCM submission established: `accepted`, `rejected`, `unconfirmed`, or `not_submitted`. Acceptance advances a still-`PENDING` delivery to `SENT`, not `DELIVERED`. If a valid receipt already advanced it to `DELIVERED` or `OPENED`, result recording preserves that stronger observation.

A **receipt** is a separately authenticated and capability-authorized mobile observation, such as `DELIVERED` or `OPENED`. Its authority is checked against the installation's current ownership. `OPENED` also records a user read; it is not inferred from FCM acceptance. Opening establishes observed delivery for interpretation, but does not synthesize a separate `DELIVERED` fact or timestamp: an opened history row can legitimately have null `delivered_at`.

Sources: [attempt reservation](../internal/dispatch/execution.go), [submission](../internal/dispatch/submission.go), [result mutation](../internal/dispatch/results.go), [observations](../internal/dispatch/observations.go).

## Read statuses at the correct level

### Campaign lifecycle

```text
DRAFT --send----------------------> QUEUED
  |
  +--schedule--> SCHEDULED --due---> QUEUED
                                     |
                               preparation
                                     |
                            PROCESSING -> COMPLETED or FAILED
```

Only drafts are editable/deletable. Expired schedules or queued work can fail without entering provider execution. Empty prepared audiences can complete without entering `PROCESSING`.

Campaign `COMPLETED` does **not** mean every device received the push. Finalization completes an empty audience or, after unfinished work clears, a campaign with at least one successful intent; a campaign with no successful intent fails. A completed campaign can therefore include failed device deliveries. `CANCELLED` exists in the schema but is not an implemented cancel workflow.

### Device delivery projection

The ordinary success path is `PENDING → SENT → DELIVERED → OPENED`; delivery can instead become `FAILED`. This is not a mandatory sequence: an early receipt can advance `PENDING` directly to an observed status. The projection is not the complete attempt history. Late authenticated observations can add receipt facts to failed work without changing its `FAILED` status, and an opened observation must not regress to a lesser observation.

Consequently, compare the result ledger, receipt facts, campaign aggregate, and statistics according to their contracts. They answer different questions.

Sources: [schedule promotion](../internal/features/applications/schedule_promotion.go), [preparer](../internal/dispatch/preparation.go), [campaign finalization](../internal/dispatch/finalize_campaign.go), [receipt SQL](../db/queries/observations.sql), [campaign history API](api/scoped-campaign-history-api.md).

## Infrastructure terms you will meet next

- **Outbox:** a database row committed with the business change, requesting later broker publication. Send and delivery-update outboxes have different purposes.
- **Idempotency key:** an actor/application-scoped key for replaying an accepted send response without accepting another send. Replaying it returns the original acceptance snapshot, not the campaign's latest status.
- **Lease / fence:** temporary worker ownership and the checks that prevent a stale owner from changing a successor's work.
- **Origin/source epochs:** lineage identifiers used to validate accepted work and its approved publication/recovery source. They are not the installation's owner epoch, and a normal restart is not a new business send.
- **Hold / sealed runtime:** a safety boundary that can block new acceptance, preparation, or execution. Do not remove it merely to make a local request succeed.

Next: [follow one campaign through these objects](notification-journey.md).
