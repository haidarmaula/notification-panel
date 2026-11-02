# ADR-05 — Device-scoped receipt evidence, truthful statistics and versioned events

**Status:** proposed, revised 2026-11-02. Not implemented or accepted.
NEW companion SPEC: `docs/specs/adr-05-receipt-ingestion.md` (not written).
See [review](../analysis/adr-04-05-review.md) and
[production plan](../analysis/production-readiness-plan.md).

**Scope:** mobile receipt route/service/auth, notifications ledger/provider
payload/statistics, worker update dispatcher, repositories/schema/sqlc, event
contract, server/worker config, mobile/API/database docs. No new Kafka topic
proposed; consumer compatibility must be verified before changing payloads.


Cross-ADR protocol: [consistency review](../analysis/adr-01-11-consistency-review.md).
Its shared lock/identity rules and verification gates apply to this proposal;
review completion does not mean acceptance or runtime implementation.

## Background and boundaries

Current routes expose no receipt path. Existing row-keyed MarkDelivered/
MarkOpened queries lack guarded callers. DeliveryUpdated has no delivery ID,
event ID or version. notification_reads has unique(notification_id,user_id).
The old proposal updated every device of one user and deduplicated outbox by
(notification_id,user_id,status), which collapses distinct device outcomes.

The previous statistics COALESCE(COUNT(...), fallback) is incorrect: COUNT
returns zero rather than NULL, and live active-user counts are not a historical
campaign snapshot. Reported metrics must state their units and evidence source.

ADR-01 owns preparation, device budgets and worker leases; ADR-02 admission;
ADR-03 sends; ADR-04 immutable registrations; ADR-06 mobile adoption. This
revision changes ADR-01's early-receipt transition contract explicitly.

## A-1. Observation semantics and attribution

### Context

Provider acceptance, application observation and a user opening a notification
are different facts. One observed device cannot speak for all a user's devices.

### Decision

DELIVERED means the reporting app/extension observed this push payload; OPENED
means it reports user interaction. Neither guarantees OS display on every
background device. Missing observation is unknown, not proven nondelivery.
The client is trusted to report honestly; receipt authorization is not hardware
attestation or proof of physical delivery.

Each receipt is bound to one immutable delivery ID and registration generation.
Never update other devices for the same user. User-level engagement/inbox reads
are separate projections: OPENED may upsert one notification_reads row;
DELIVERED alone never marks a notification read. Opening implies observed for
that device; a later DELIVERED report cannot regress OPENED.

Application receipts are the v1 evidence source. Firebase also offers delivery
reporting/export facilities with coverage/configuration/latency constraints;
they are not a synchronous receipt in the Send response. Do not claim no such
telemetry exists. Optional reconciliation/export is deferred, not invented as
an always-available per-device callback.

### Alternatives rejected

User-wide device mutation inflates delivery counts. Counting SENT as physical
delivery or treating missing callbacks as failure produces misleading metrics.

### Consequences

Client reporting coverage and app-version adoption must accompany engagement
metrics. Validate foreground/background/tap behavior per platform in ADR-06.

## A-2. Receipt authorization and API contract

### Context

A user JWT plus guessed notification ID does not demonstrate which device saw
the payload. New receipts should reuse the bearer trust boundary already used
by /device-tokens rather than copy legacy /mobile/sync body-JWT behavior.

### Decision

NEW POST /api/v1/mobile/notifications/{id}/receipt uses Laravel mobile bearer
authentication. Require explicit algorithm, nonempty external_id, expiration,
configured issuer/audience and user status policy. Factor one verified claims
contract shared with modern registration; do not substitute staff JWT or a
shared application API key for user identity. Existing /mobile/sync is migrated
separately; current middleware does not yet enforce all these claims.

Proposed body: delivery_id (opaque), event (DELIVERED or OPENED), receipt_token,
client_event_id (stable UUID for retry), optional observed_at (RFC3339).
Notification ID must match the delivery. Authenticate, validate bounded input,
then verify capability, original binding identity and current installation
ownership epoch under the same transaction lock order as A-3. A same-owner
address refresh may still report an earlier binding's push; logout or account
switch invalidates the prior owner epoch, including a later switch back to
the same account. A new owner cannot report the old user's delivery.

Worker creates a signed, versioned capability scoped to delivery ID, immutable
registration ID/generation, installation owner_epoch and expiry. Proposal:
expiry deadline_at +7d, separate from push TTL. Using the persisted send deadline
avoids expiry before a future scheduled send; the 7d observation window remains
a proposal, not an approved retention policy. Dedicated receipt-signing keys (key ID and rotation
overlap) are shared only by worker signer/server verifier, not Laravel/staff
JWT keys. Exact encoding/MAC domain/constant-time verification and key lifecycle
belong in SPEC/ADR-11. Capability is opaque, redacted from logs, analytics,
traces and public admin APIs. Include environment/application scope in the MAC
domain and never reuse receipt-signing keys between staging and production.
It is created before sending and stable across
attempts. Never expose it through an authenticated user's arbitrary delivery
listing, which would undermine evidence of payload possession.

The server resolves user/registration from persisted state, never from client
user_id or installation ID alone. A valid receipt proves possession of the
payload capability and a valid user session; a compromised client/key can still
forge its own reports. Attestation/abuse policy is ADR-11's release gate.

Observed_at is optional client metadata, not transition/order authority. Reject
malformed or timestamps outside delivery-created..capability-expiry plus a
small proposed 5-minute skew; validate future bound against server time too.
Persist server received_at as authoritative observation time. Do not discard
otherwise valid offline reports merely because more than 24h elapsed; use the
explicit capability/retention window. Never reorder state using client clocks.

| Response | Meaning / client action |
| --- | --- |
| 200 receipt recorded/acknowledged | Evidence durable or exact/semantic duplicate; client may remove its queued report |
| 400 | Malformed body/ID/event/time; fix request |
| 401 | Missing/invalid/expired mobile auth; obtain current session, retain report only for same owner |
| 404 | Uniform unknown/not-owned/invalid capability or obsolete binding; no existence oracle |
| 409 | Same client_event_id reused with a different canonical receipt body |
| 410 | Capability expired, only after ownership/signature checks; terminal report expiry |
| 413 / 429 | Body limit / rate limit; respect Retry-After on 429 |
| 500 internal server error | No success guarantee; retry same client_event_id with backoff |

TLS, payload limits, rate limits by principal/installation/IP and key rotation
must be active before exposing the route. No staff audit action is fabricated;
persist receipt/security telemetry without secrets. Every DB failure uses the
literal internal server error externally. Do not write users during receipts.

### Alternatives rejected

A device ID alone is enumerable metadata. A body user_id can impersonate another
principal. Returning 200 for an early report without storing it loses evidence.

### Consequences

This is a new contract; no existing receipt client needs legacy body-JWT
compatibility. Identity claims, capability lifetime and offline behavior need
mobile/security review before SPEC acceptance. Do not ask users to paste keys.

## A-3. Durable evidence and monotonic transitions

### Context

A fast device can report OPENED before the worker persists its provider result,
or after an uncertain attempt has been finalized FAILED. Those are real races.

### Decision

In one bounded transaction, take required authority locks under shared CR1,
then campaign, installation, immutable binding and delivery.
Registration mutations also lock installation before device rows;
all multi-installation locks use stable order. Recheck auth/ownership/version
under lock. No network calls or worker lease renewal on the receipt path.

NEW receipt request dedup stores (authenticated principal, client_event_id)
and a canonical request fingerprint/result. Reuse with a changed body returns
409. NEW observation facts are unique(delivery_id,event), so a second client
ID for the same fact is also a semantic no-op. Bound request-dedup retention
and ingress rate; this is not unbounded forensic logging.

Persist observation fact, accepted request result, effective ledger mutation,
OPENED user-read projection and update outbox **in one transaction**. If any
write fails, roll back and return retryable error. 200 always means stored or
already stored, never silently pending. State policy:

| Current delivery | Valid observed event | Effective result |
| --- | --- | --- |
| PENDING or SENT | DELIVERED | DELIVERED; store observation, suppress further provider retries |
| PENDING, SENT or DELIVERED | OPENED | OPENED; record observed/opened facts and upsert user read |
| OPENED | DELIVERED or OPENED | No regression; persist missing dedup/fact only, no duplicate state event |
| DELIVERED | DELIVERED | Idempotent observation acknowledgement |
| FAILED | DELIVERED or OPENED | Keep terminal submission disposition; retain late observation and user read if OPENED; expose discrepancy |

Promotion from PENDING is permitted **only** with the authenticated signed
payload capability, not arbitrary caller assertions. It closes the early-receipt
race and avoids unnecessary retry of an already-observed push. It does not
manufacture provider_message_id or provider_accepted_at. A later valid-lease
worker response may record confirmed acceptance metadata without regressing
the observed state; a late failure cannot overwrite DELIVERED/OPENED.

FAILED stays terminal for retry; a late observation is separate evidence, not
automatic resubmission. If the campaign already finalized, record late evidence
without reopening its submission lifecycle. This also applies while the campaign
is PROCESSING: a FAILED row's late observation does not become a successful
submission disposition. ADR-01 aggregates effective SENT/DELIVERED/OPENED
states, not raw observation flags on FAILED. Expose the disagreement explicitly
in statistics, including overlap between failed submissions and observations.

The request fingerprint includes normalized event/delivery/observed_at and
capability identity. Duplicate handling must reauthorize current principal;
expired/reassigned sessions do not retrieve historical success through dedup.
Same-owner address refresh does not revoke a receipt capability; the original
binding/generation remains immutable and the owner_epoch must still match.
Observed_at never rewrites first server observation timestamps.

### Alternatives rejected

Returning 200 on PENDING without persistence loses the only callback. Blind
PENDING->SENT worker writes regress early receipts. Updating every device or
marking DELIVERED as read confuses device evidence with user engagement.

### Consequences

ADR-01 A-5/A-6/A-10 must adopt these guards; a receipt is not a provider worker
and does not claim its lease. Its transactional lock coordinates with fenced
worker writes. Add fault/race tests before relying on this in production.

## A-4. Snapshot-based statistics with explicit units

### Context

Counts from current active users change after a campaign; mixed user/device
units make percentages misleading and raw COUNT never invokes a NULL fallback.

### Decision

During complete atomic fan-out persist audience_user_count (resolved users),
eligible_user_count (users with an eligible device) and eligible_device_count.
These are immutable snapshot totals, including known zero. New structured
statistics expose schema version, snapshot completeness and counters with units:
targeted_users, eligible_users, targeted_devices, pending_devices,
provider_accepted_devices, submission_failed_devices, observed_devices,
opened_devices and opened_users. Provider acceptance uses persisted response
evidence, not an inferred SENT count after an early observation.

Observed counts come from device facts; OPENED implies observed for that device.
Opened users count distinct original owners and read projection is written only
by OPENED. A failed submission with late observation contributes to both
submission_failed_devices and observed_devices; these are different axes.
Expose discrepancy count and collection coverage, not a false disjoint total.

Draft/incomplete/unknown legacy snapshots use explicit unavailable/null state,
not fabricated zero or a live-population fallback. Migration may establish a
legacy lower bound but must label it as such. No resending to reconstruct stats.
Counters must use consistent DB reads and avoid join multiplication. Preserve
existing API fields only under documented legacy semantics/deprecation; add a
versioned statistics object and contract tests before frontend consumes it.

### Alternatives rejected

COALESCE(COUNT(...), active_users) is not a valid fallback. One user's OPENED
must not become N opened-device facts. Lifetime current user count cannot
reconstruct historical campaign audience.

### Consequences

ADR-01 preparation schema grows snapshot counters. Read-model/API migration
must be reviewed with frontend owners; backend counts do not claim total OS
delivery. Receipt gaps are observable unknowns, not success/failure guesses.

## A-5. Per-delivery versioned event outbox

### Context

The old unique(notification,user,status) outbox merges devices and loses its
dedup memory when rows are deleted. Multi-replica drain can reorder changes.

### Decision

Every publishable delivery state/evidence mutation atomically increments that
delivery's monotonic delivery_version within the approved source_epoch and
inserts NEW notification_update_outbox with
unique(source_epoch,delivery_id,delivery_version) and a generated stable event_id.
Persist event ID and immutable full projection payload at transition time;
publish retries never generate a new ID/timestamp. Semantic duplicate receipts
do not increment version or recreate an event after the row was deleted.
Attempt counters/local retry scheduling alone are not new status events.
OPENED implies observed delivery in the full projection. A later DELIVERED fact
may add deduplicated evidence, but creates no new version if no publishable
projection field changes. SPEC defines those fields and timestamp normalization;
raw request arrival alone is not a projection mutation.

Proposed additive DeliveryUpdated schema_version=2 includes origin_epoch,
source_epoch, event_id, delivery_id,
delivery_version, event_kind, existing notification_id/user_id/provider/status,
nullable provider message/acceptance evidence, observed flags/times, server
occurred_at and stable reason code. No raw address, installation credential,
receipt capability or client JWT. event_kind distinguishes submission changes
from observations/late evidence. status is the current effective ledger state;
late OPENED on FAILED appears as status FAILED plus observation facts, not a
fake state transition. Full payload shape is fixed in SPEC and consumer review.

Worker-only dispatcher uses one row per bounded transaction, broker ack then
delete, same protocol as revised ADR-03. Claim only a due row with no older
pending version for that delivery (including a row locked by another drainer),
using an indexed anti-predecessor predicate with FOR UPDATE SKIP LOCKED.
Deferral of one version blocks later versions of that delivery, not others.
Multiple replicas cannot publish a higher version merely by skipping its
locked predecessor. Bounded per-row/time budgets, persisted failure backoff,
fairness, backlog-age alerts and shutdown follow ADR-03/08.

Reuse KAFKA_UPDATE_TOPIC and existing delivery_<notification_id> key. Partition
key does not establish application exactly-once or total event ordering.
Consumers dedup stable event_id and persist highest delivery_version within
the approved (source_epoch,delivery_id) checkpoint; lower/equal versions cannot
regress projections. Epoch activation/restore reconciliation follows ADR-10,
never arbitrary publisher input. A version gap may apply the full higher
snapshot or trigger reconciliation according to the reviewed consumer contract.

[ADR-10](adr-10-retention-recovery.md) adds tagged recovery snapshots and
erasure/tombstone propagation/checkpoints to the event-contract review. They
are not normal sends or new notification status literals. Preserve original
event identity/provenance, do not silently relabel old pending events, and do
not infer that restoring a DB also rewound Kafka or downstream state.

Partition affinity and low-latency synchronous publication require the producer
changes in [ADR-07](adr-07-kafka-topology.md), not the current LeastBytes/default
batch behavior. A cancelled WriteMessages can still publish later; old copies
may arrive after a higher version despite the DB predecessor guard. Stable
IDs and the consumer's monotonic version check are therefore mandatory.

A publish ack followed by failed delete commit can duplicate an event; stable
ID/version permits safe replay even after later versions. Inventory downstream
consumers, deploy additive parsing/version support first, then enable v2
production. Never assume consumers exist or do not exist from repo evidence.
If incompatible consumers cannot upgrade, choose a reviewed versioned topic
migration in ADR-07; do not silently dual-count the old and new streams.

### Alternatives rejected

Provider message ID is not a reliable device-event key. A user-status unique
index drops device facts. SQL ordering alone does not serialize concurrent
drainers. Regenerating IDs on publish prevents consumer deduplication.

### Consequences

The old no-wire-change claim is withdrawn. Additional fields and event semantics
need integration sign-off. No blind backfill of every historic delivery as a
new observed event; use tagged reconstruction with known coverage if approved.

## A-6. Schema, config and rollout boundary

### Context

These semantics require durable identity/evidence in addition to an outbox.

### Decision

NEW migrations cover opaque delivery identity, delivery_version, acceptance
metadata, receipt facts/request dedup, snapshot counts, read upsert and update
outbox with due/version indexes. Preserve delivery/registration references
through controlled tombstones; do not erase pending facts with user/device
FK cascades. Exact retention/anonymization/key lifetime is ADR-10/11's release
gate. Choose sequential migration numbers in SPEC, always up/down pairs before
sqlc regeneration; never hand-edit generated files.

Server receives receipt verification key ring, limits and identity validation
config; worker receives signing key and UPDATE_OUTBOX_POLL_INTERVAL (proposed
1s) plus bounded publish budgets. Firebase send credentials remain worker-only.
Shared receipt keys are separate from mobile JWT secrets and rotated with
overlap at least the accepted capability lifetime plus skew. Health checks
never send real pushes or invent receipt evidence.

Update API docs only with clear proposed/implemented status; implementation
must update routes/module/service Godoc, events.go, database docs and verified
code-review findings. Review mocks based on actual interfaces changed.

### Alternatives rejected

Treating this as one outbox-only migration omits identity/evidence needed for
correctness. Dropping pending outbox/facts is not an operational rollback plan.

### Consequences

Still open before SPEC acceptance: exact mobile contract/versions, JWT claims,
capability/offline lifetime, key/PII retention, attestation policy, downstream
consumer inventory and integration testing ownership. No runtime or live
receipt guarantees follow from design revision.

## Primary evidence and limits

[Firebase delivery reporting](https://firebase.google.com/docs/cloud-messaging/understand-delivery)
documents optional reporting/export facilities; this proposal deliberately uses
explicit client observations. SDK send results are submission outcomes, not
an authenticated callback to this backend. Official docs and pinned Go SDK
were inspected on 2026-11-02. No mobile repo/device, live provider or connected
consumer was tested; Orbit discovery could not resolve DNS in this session.

## Design verification cases for the companion SPEC

| Input / condition | Expected observable result |
| --- | --- |
| One user, two devices, one receipt | Only matching device facts change; one OPENED user projection if relevant |
| Valid JWT, guessed delivery, no valid capability | No mutation or existence leak |
| Different/current replacement owner | Old generation receipt rejected, even if it has the old payload |
| Same owner refreshes FCM address | Old binding's valid payload receipt still accepted while owner_epoch matches |
| OPENED before provider result | Fact stored, PENDING promoted, retries suppressed; later worker cannot regress |
| Worker failure and receipt race | Serialized locks; observed success not overwritten, or late evidence retained on terminal FAILED |
| DELIVERED then OPENED/reverse order | Monotonic device facts, no duplicate read/state event |
| Duplicate UUID, different receipt body | 409, original result/facts unchanged |
| Duplicate observation after outbox deletion | 200 acknowledgement, no new version/event |
| DB error after observation write | Entire request transaction rolls back; retry can persist all effects |
| Device offline >24h but within signed window | Valid owner/capability report accepted; no arbitrary 24h cutoff |
| Capability expiry | Owner-authenticated 410; no false 200 or foreign-data disclosure |
| Population changes after campaign | Snapshot denominators unchanged; unknown legacy count remains labeled |
| OPENED on terminal FAILED | Evidence/read retained, no lifecycle reopening; discrepancy visible |
| Two drainers, adjacent versions | Higher version waits for predecessor; other deliveries make progress |
| Ack then crash or stale event arrives | Same event ID/version on replay; consumer projection never regresses |
| Old downstream consumer | Compatibility gate prevents unreviewed schema-v2 enablement |
