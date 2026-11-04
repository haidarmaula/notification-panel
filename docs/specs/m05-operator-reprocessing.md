# M05 operator reprocessing

Status: implementation in progress, 2026-11-04. Migration 000025 and generated
persistence primitives implement slice 1; see the
[verification record](../analysis/m05-replay-persistence-verification.md). No replay
CLI, operational credential issuer or production activation is implemented.
[Scoped request registration](../analysis/m05-replay-registration-verification.md)
is available as an unwired library through migration 000026; per-position replay
broker execution remains unimplemented. [Per-position claims](../analysis/m05-replay-claim-verification.md)
now commit attempt/control/audit atomically through migration 000027. [Dry-run inspection](../analysis/m05-replay-inspection-verification.md)
implements advisory eligibility without mutations. It refines [ADR-02 A-4](../adr/adr-02-worker-consumption.md#a-4-operator-replay-and-retention),
[ADR-11](../adr/adr-11-security-baseline.md) and
[ADR-12](../adr/adr-12-multi-application-isolation.md).

## Purpose and boundaries

An authorized operator can request a new admission event for explicitly selected
quarantine evidence. Replay helps recover accepted work; it never authorizes a
new campaign, rebuilds a committed audience, resets a delivery budget or calls
FCM. Normal [QUEUED recovery](m05-queued-recovery.md) remains the preferred
background recovery path. Replay does not seek Kafka offsets or restore ordering.

The first interface is a restricted `cmd/requeue`, not an HTTP endpoint. Require
one application UUID, one request UUID, a change/incident reference and 1–100
explicit evidence positions. Each position is `(log_source_id, partition,
offset)`, the existing primary key; there is no assumed numeric dead-letter ID.
Reject duplicate positions and malformed input before database work. Process
serially, committing each position independently; a batch is not atomic.

A dry run performs bounded, authorized reads and reports classifications, without
claims, publication, resolution or campaign changes. Its output is advisory;
execution must revalidate everything. Read access to raw evidence remains
restricted. Neither mode prints raw keys, payloads, tokens or credentials.

## Authority

Require a verified, short-lived operational credential with explicit replay
purpose, environment/deployment, application, actor, expiry, grant identifier and
step-up evidence. A caller-supplied staff ID, role claim, shell access or ordinary
staff access JWT is insufficient. The trusted issuer and deployment key policy
must be configured and tested before enabling the CLI; absent configuration
fails closed. Never ship a permissive local-admin fallback.

Additionally, the service requires a current active staff identity, ADMIN or
SUPER_ADMIN send authority, and an explicit active grant for the exact app.
SUPER_ADMIN has no cross-project wildcard. A separate revocable replay grant
binds the verified operational credential to the app and source selection.
Grant issuance/revocation uses a restricted operational channel, separate from
ordinary staff CRUD. The credential must remain valid through the bounded
publication attempt. Issuance integration and step-up verification are release
gates, not supplied by a `--actor` option. The
[credential contract](m05-replay-credential.md) defines the local pinned Ed25519
verifier and complete request fingerprint. Database authority and revocation
checks remain separate mandatory transaction work.

Unauthorized and out-of-scope positions return the same redacted classification.
Do not expose a decoded application or campaign identity before authority and
explicit app scope agree. Invalid evidence has no trusted application attribution:
a scoped request can record its refusal but cannot authorize replay of it.

## Evidence and eligibility

Source evidence is immutable. Decode the stored key/payload with
`kafka.DecodeScopedSend`; do not repair JSON, substitute a key, infer missing
scope or copy the stored bytes directly to Kafka. Re-encode only the validated
`ScopedSendRequested`, retaining its canonical `notification_<id>` key.

Require a known, active source registration and verified deployment manifest for
its cluster/topic incarnation. Publication uses the configured current approved
send source. Ordinary replay requires the historical source registration to be
that same source; retired/recreated topics need a separate reviewed recovery
workflow. An operator flag cannot approve an incarnation or change partitioning.

Require an unsealed deployment, matching current origin/source epochs and exact
application scope. Match all immutable acceptance evidence against the scoped
campaign: notification ID, queued time, optional scheduled time, deadline and
both epochs. Never stamp current epochs onto old evidence or extend a deadline.
Missing campaigns and acceptance mismatches remain unresolved and unpublished.

After exact matching, classify under locks:

| Condition | Result | Publication/resolution |
| --- | --- | --- |
| Existing committed replay resolution | Already resolved | No publish; return prior disposition |
| Complete preparation marker with matching campaign epochs and status PROCESSING/COMPLETED/FAILED | Already prepared | Resolve as admission no-op; preserve snapshot/intents |
| COMPLETED or FAILED, no preparation marker | Terminal | Resolve as terminal no-op; never resend |
| QUEUED and expired by database time | Expired | No publish; leave unresolved for canonical QUEUED recovery to terminalize |
| QUEUED, current lineage, unexpired and eligible | Replay eligible | Claim, then revalidate before publishing |
| DRAFT, SCHEDULED, CANCELLED, or PROCESSING without complete preparation | Reconciliation required | No publish; unresolved |
| Migration hold, project/policy hold, inactive app, unapproved coverage, or a future preparation retry | Held | No publish; unresolved |
| Unsupported target metadata or malformed/mismatched envelope | Rejected | No publish; unresolved |

A marker with mismatched campaign epochs, or a marker on QUEUED/DRAFT/SCHEDULED/
CANCELLED work, requires reconciliation and remains unresolved. These consistency
checks precede terminal/no-op classification, matching the canonical Preparer.

No-op resolution still requires scoped operational authority and matching
lineage. It may proceed during a new-work hold, but never while sealed. Eligibility
must inspect supported target metadata without creating intents. Audience/coverage
is ultimately rechecked by the canonical Preparer; replay approval alone does
not establish a snapshot or successful delivery.

## Persistence and lock order

Add paired migrations and generated SQL before implementation. Preserve the
existing evidence row and physical-position uniqueness. Store replay attempts
separately, with an optional resolution referencing the successful attempt.
An attempt records scoped actor/grant, request UUID, evidence position, safe
change reference, original acceptance identity, database timestamps, claim token,
lease expiry and an allowlisted outcome. It does not copy raw evidence.

Create an immutable request header unique on `(application, request UUID)` before
position processing, storing a fingerprint of the full normalized selection,
verified actor/grant and safe change reference. Per-position attempts additionally
use unique `(application, request UUID, evidence position)` keys.
The same request with different selection/reference is a conflict. A repeated
identical request returns its durable per-position outcomes; it does not start a
new publication attempt. An explicit later retry uses a new request UUID.
Only one unexpired claim may exist for an evidence position across all requests.
Enforce this through one locked control row per physical position, not a
time-dependent partial unique index. Authorized held/rejected/no-op results are
also persisted with audit so repeating a request cannot change their disposition.
Unauthorized attempts must not reserve a request key or claim evidence.

A claim transaction has a five-second operation bound. Lock in this order:
runtime, source registration, app, staff authority/explicit app and replay grants,
dispatch policy, campaign, evidence/replay control. Verify implicit foreign-key
locks against existing staff/grant/campaign mutations before landing SQL.
Compare leases and deadlines with database time. Claim ownership uses a random
unguessable token and a 30-second lease. Commit the started attempt and audit
before any broker call; an uncertain claim commit must be reconciled by request
key, never followed by an immediate blind publish.

A second five-second transaction uses the same lock order, reloads the immutable
evidence and repeats authority, control, lineage, campaign, deadline and claim
checks. A stale token, expired lease or resolved position cannot publish. Keep
these locks until publication/result commit so concurrent control changes
serialize with the attempt. Require at least five seconds remaining on the
credential and lease before the broker call; do not renew a lease inside a
publication attempt. The broker call is synchronous, bounded to two seconds and
made at most once by this transaction. A publisher must honor cancellation and
must not return while a detached publication remains active.

Do not automatically retry a transaction containing broker I/O. Successful broker
ack commits a `published` attempt, resolution and audit atomically; this means
broker acceptance only. A publish error is `publish_unconfirmed`, releases the
claim and leaves evidence unresolved. Even a timeout may have reached Kafka.
Unknown result-commit outcome is reported as uncertain and reconciled from the
same request. A crash leaves the durable started attempt; after lease expiry a
bounded service-owned reconciler records `abandoned_unknown` with audit and
releases the claim. Cleanup grants no publication authority and does not require
the original operator to remain active. A stale
finalizer cannot overwrite a successor's outcome.

Cleanup has an independent one-second context. Claim/result/audit database
failures return a sanitized error and preserve uncertainty; no later position in
the batch is published after an infrastructure failure. A broker-unconfirmed
result also stops that batch. Logical held/rejected/no-op positions may continue.
Already committed per-position progress remains reportable.

## Audit and outcomes

Register new actions in `internal/audit/constants.go` at implementation. Record
request/attempt, scoped actor/grant, source position, safe reason/reference and
allowlisted result. Started attempts and terminal results must have atomic audit
evidence. Evidence claimed by another live request reports busy without takeover.
An audit failure rolls back the corresponding database mutation.

A crash after broker acceptance but before result commit can cause a later
explicit replay to publish again. This is at-least-once admission: preparation
deduplication must preserve audience, intent identities, counts, deadline and
provider budgets. No exactly-once FCM or device-delivery claim follows.
Evidence, attempts and resolutions remain subject to ADR-10 retention/PII policy;
no automatic purge or deletion-based resolution is introduced.

## Implementation slices and acceptance evidence

1. Replay request/attempt/claim/resolution schema, constraints and SQLC queries;
   guarded down migration refuses retained replay evidence.
2. Scoped eligibility and claim/result transactions, durable audit, injectable
   synchronous publisher and bounded stale-claim reconciler.
3. Operational credential verification and revocable grant checks; no executable
   mutation path until these guards exist. Restrict issuance separately.
4. CLI dry-run/execute/reporting and configuration validation, then opt-in
   PostgreSQL/Kafka fixtures. Wire worker/scheduler only after their own gates.

Required tests before calling this slice locally verified:

- Malformed/ambiguous payload, wrong key/app/acceptance fields, stale/future epochs,
  retired source, missing operational permission and cross-project SUPER_ADMIN
  all publish zero messages; output contains no raw evidence or credentials.
- Inactive/revoked staff/app/replay grant and expired step-up credential fail;
  revocation/control races serialize consistently with the documented locks.
- Terminal/prepared no-ops, empty committed audience, due/held/expired QUEUED work,
  unsupported targets, QUEUED with a marker, mismatched marker epochs and
  PROCESSING without a marker preserve canonical state.
- Same request retry, conflicting selection, two operators, expired claims and
  stale finalizers produce at most one live publication attempt per position.
- Faults before claim commit, before publish, broker timeout, broker ack followed
  by result/audit failure, process crash and lease expiry retain recoverable
  evidence. Redelivery adds no snapshot/intent or budget reset.
- Database/publisher stalls honor bounds, cancellation releases resources, a later
  failure preserves earlier batch results, and no later publish passes an unsafe
  infrastructure outcome.

Document local fixture results separately from issuer integration, real broker
manifest verification, deployment ACLs and production recovery exercises. This
contract alone closes none of those gates.
