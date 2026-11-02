# ADR-10 — Data lifecycle, backup and coordinated recovery

**Status:** proposed, 2026-11-02. Not implemented, policy-approved or restore-tested.
NEW companion SPEC: `docs/specs/adr-10-retention-recovery.md` (not written).
Gates: [API/data/security readiness](../analysis/api-data-security-readiness.md).

**Scope:** data classification/retention, FK/tombstone/deletion workflows,
maintenance jobs, archive/read contract, PostgreSQL backup/WAL/restore,
Kafka/consumer coordination, recovery epochs and runbook evidence. No data is
deleted, exported, copied or restored by writing this document.


Cross-ADR protocol: [consistency review](../analysis/adr-01-11-consistency-review.md).
Its shared lock/identity rules and verification gates apply to this proposal;
review completion does not mean acceptance or runtime implementation.

## Evidence and policy boundary

Current user/device/delivery/read FKs cascade in places; audit actor FK restricts
staff deletion. Docker volumes are local persistence, not an inspected backup
system. No working backup/restore/archival job was found in inspected Makefile/
deployment files. External company backups may exist; they have not been checked.

User confirms company-hosted Kafka, not DB hosting/backup architecture. Business
retention periods, privacy/deletion obligations, RPO (maximum tolerated lost
data interval) and RTO (time to restore safe service) are unknown. Do not invent
approved days or claim compliance. ADR-01–09 supply correctness invariants;
retention must preserve them while minimizing sensitive data.

## A-1. Explicit retention classes and correctness floors

### Context

One generic delete-old-rows job can erase live work, replay protection, user
evidence or keys before their associated validity window closes.

### Decision

Define versioned policies with owner, purpose, cutoff source, minimum safety
floor, approved lifetime and deletion/archive action. Business lifetimes have
no silent infinite/default duration at production enablement. Proposed 24h send
idempotency, 7d receipt capability and Kafka 7d/30d windows remain unapproved
design inputs; do not mislabel them final policy.

| Class | Minimum correctness rule | Policy decision needed |
| --- | --- | --- |
| Pending intents/active leases/unpublished outbox | Never age-purge live work; finalize/reconcile first with explicit outcome | Operational maximum age/escalation and shutdown/timeout evidence |
| Immutable binding/address snapshot | Retain what pending work needs; then remove encrypted/raw addresses promptly under approved policy | Active/inactive address lifetime and data-key ownership |
| Send idempotency result | Keep through declared expiry; current notification guard still applies after expiry | Final TTL and cleanup cadence |
| Receipt request dedup/facts/keys | Preserve accepted report window and semantic dedup; never re-create purged delivery via receipt | Offline/report lifetime, audit purpose and rotation overlap |
| Terminal delivery/campaign/read evidence | Keep required historical snapshot/version until report/replay windows end | Business history duration and archive/deletion semantics |
| Quarantine/raw bytes/replay evidence | Unresolved is not disposable by age alone; resolve or approved terminal disposition | Raw evidence size/PII minimization and resolved lifetime |
| Audit/security records | Field allowlist, restricted access, protected actor tombstone | Purpose-specific lifetime/holds/deletion authority |
| Kafka log/consumer dedup | Align ADR-07 replay window, consumer versions and reconstruction policy | Outage/catch-up and external consumer needs |
| Logs/metrics/traces | No secrets/PII by default; bounded storage | Diagnostic retention/access and cost |
| Base backup/WAL/snapshots | Complete recoverable chain and available approved keys, separate failure domain | Backup/PITR window, immutability, RPO/RTO and erasure lifecycle |

Expired key cleanup is storage maintenance, not a shortcut around authenticated
replay or a guarantee that new sends are allowed. Receipt signing-key expiry
and data encryption-key destruction are different operations. Emergency key
revocation may end capability acceptance early and must be explicit/audited.

### Alternatives rejected

Deleting by created_at alone ignores active intent. Retaining every token/raw
payload forever is not required for aggregate analytics. Arbitrary audit/backup
day counts without an owner are not an approved policy.

### Consequences

Policy values block production maintenance/SPEC acceptance, not continued
architecture work. Protect pending work with constraints/state checks, not only
an operator's memory.

## A-2. Controlled deactivation, erasure and bounded maintenance

### Context

Cascade deletion destroys the evidence and generation fences needed for safe
retry; retaining the whole user forever is not an acceptable deletion design.

### Decision

Device delete deactivates its binding under owner/version checks. User removal
first blocks new eligibility, advances/revokes binding ownership, holds/finalizes
unsent pending work with a registered safe reason, and waits for/reconciles
leases/in-flight uncertainty. It cannot recall external accepted pushes.
Keep minimized non-reusable tombstone identity where needed; remove or anonymize
PII and raw destinations under the approved policy. Do not reuse old IDs for a
different owner. Replace destructive FKs with the reviewed retained-identity/
restrictive workflow before enabling modern delivery.

Critical erase/export/hold/replay actions require ADR-11 scoped operator
authorization and audit. Erasure/restriction signals to consumers use a reviewed
tombstone/evidence contract with durable version/checkpoint so old Kafka/archive
replays cannot silently recreate deleted projections. Existing event v2 must
be extended/reviewed before consumers are expected to handle that kind; do not
invent a live event or assume consumers already implement it.

NEW maintenance jobs use bounded keyset batches, state predicates, short
transactions, stable watermarks, idempotent progress and dry-run counts. Exclude
active leases/pending outbox/unresolved holds; index cutoff queries, bound lock/
statement duration, limit IO/WAL and pause on unsafe load. Persist job outcome/
reason/count without copying sensitive payloads. Purge failure retains evidence
and alerts; no nightly unbounded table delete or CASCADE shortcut.

### Alternatives rejected

Blind FK cascade breaks ongoing work. Soft deletion retaining all PII indefinitely
is not erasure. A DB flag cannot erase already exported provider/analytics data.

### Consequences

Deletion scope includes secondary stores, archives and backups under owner
policy. Restore must reapply a protected erasure/revocation ledger before
exposure; ledger retention is at least the approved reintroduction window.
Technical tombstones are not an assertion of legal anonymization.

## A-3. Archive/read behavior and truthful historical statistics

### Context

Archiving data without query/read policy can silently change old campaign totals
or turn an archived delivery into a nonexistent/eligible-to-resend record.

### Decision

Start with indexed retention and measured storage growth; do not introduce
time partitioning solely because it is fashionable. If archival/partitioning
is justified, specify uniqueness/FK/index implications, atomic visibility
watermarks, archive manifest/checksum, restore/read access and schema versions.
Do not drop an active partition containing live work or required checkpoints.

Persist immutable campaign denominators and finalized aggregates with clear
coverage and late-observation policy. Distinguish complete history, approved
aggregate-only history, archived detail and expired detail. API returns a
documented coverage state, never live-user fallback or fabricated zero.
User reads/engagement and device submission remain separate units (ADR-05).
Late receipts after detail retention/expiry receive a documented terminal result;
they do not restore deleted user data or allocate new intents.

### Alternatives rejected

A pure archive dump with no read contract makes reporting inconsistent. Counting
only remaining hot rows undercounts history. Replaying to regenerate stats can
send notifications again and is not a reporting operation.

### Consequences

Before partition/archive migration, prove list/count/stat/replay compatibility
and query cost. Aggregate retention also needs a purpose and owner.

## A-4. PostgreSQL backup and recovery evidence

### Context

An existing volume or replica is not a verified independent recovery point.
WAL-based recovery requires a usable base backup and complete archive chain.

### Decision

Proposed baseline is encrypted base backups plus continuous WAL archiving/PITR,
stored outside the DB/sole server failure domain with restricted access and
protection against accidental overwrite/deletion. Add a separate failure-domain
copy where the approved recovery model requires it. Choose vetted tooling and
versions after inspecting DB hosting; no backup job/tool is assumed to exist.

Record backup manifests, database/system/timeline/schema/version, checksums,
archive continuity, key IDs (not keys), restore prerequisites and retention
watermarks. Monitor backup/WAL freshness/gaps and disk pressure. Never delete
pg_wal manually as a recovery shortcut. Store required decryption/config assets
through a separate controlled key recovery process; a backup without usable
approved keys is not a restore result.

RPO/RTO and PITR window must be explicitly accepted and tested. Measure restore
to safe usable service including integrity, security/erasure, Kafka/projection
and external-send reconciliation, not only pg_restore/base-backup completion.
Logical dumps may supplement portability/verification but do not automatically
meet the agreed PITR/RPO target.

### Alternatives rejected

Backup on the same only server fails with that server. Replication copies
accidental deletes and is not history. Successful backup exit code without
isolated restore evidence does not prove recoverability.

### Consequences

Runbook needs owners, failure-domain/storage inventory, access/rotation,
observability and repeated isolated restore drills. No data backup is executed.

## A-5. Sealed restore, epochs and cross-system reconciliation

### Context

Restoring PostgreSQL can rewind attempt counts, revocations, IDs and delivery
versions while Kafka/FCM/mobile/consumers remain ahead. Replaying old outbox
blindly can resend or resurrect previously deleted state.

### Decision

Restore into isolated **sealed recovery mode**: outbound FCM/legacy sends denied
outside restored DB controls, admission/admin mutations restricted, no active
production consumer group membership or automatic outbox drain. A restored
dispatch_enabled=true must not bypass this out-of-band gate. Verify integrity,
schema, leases, time and backup timeline before any external connection.

Reapply authoritative erasure/security/key revocations that happened after the
recovery point. Invalidate staff sessions/refresh families and verify privilege/
credential changes; restored old permissions must not silently re-enable access.
Revalidate uncertain mobile ownership/epochs before resuming affected bindings.
Review Kafka retained ranges/committed offsets, external accepted/unknown work,
consumer versions and old pending outbox with the incident owner. Do not reset
attempts/deadlines or pretend a DB snapshot undoes an external push.

Use distinct identities before production:

- **origin_epoch:** immutable accepted business namespace on notification/intents
  and both send/update envelopes. Match it with notification_id; recovery does
  not relabel an existing accepted intent into a different business identity.
- **source_epoch:** approved publication/projection recovery lineage in envelopes
  and consumer checkpoints. Stable across ordinary restarts; only reviewed
  recovery activation changes it. A rebuilt projection may have a new source
  epoch while preserving the accepted origin_epoch.
- **log_source_id:** physical Kafka cluster/topic incarnation for source offsets
  and quarantine uniqueness (ADR-02/07), independent of both logical epochs.
- Installation **owner_epoch** and migration **route_epoch** remain separate
  authorization/rollout versions, never substitutes for these identities.

No epoch is newly generated on every boot. On recovery, compare restored metadata with
the current deployment control/evidence; a mismatch holds execution. A reviewed
epoch transition/reconciliation manifest maps preserved business identities,
Kafka boundaries and consumer rebuild policy. Old/future-epoch records are
quarantined/reconciled, not executed against a reused numeric notification ID.

Versioned consumer keys become (source_epoch,delivery_id,delivery_version), with
stable global event-ID dedup. A lower restored version cannot silently overwrite
a newer external projection. Rebuilding needs explicitly tagged current-state
recovery snapshots/authorized epoch activation, not ordinary new increments or
blind relabeling of an old immutable event payload. Old original event IDs and
evidence remain unchanged; rebuilt events have a distinct identity/provenance.
Consumers and ADR-05's schema-v2 rollout must adopt this before enablement.

Prevent reuse of externally observed DB ID ranges after restore. Advance sequences
only against verified high-water evidence; max(id) from the restored snapshot
alone is insufficient. If the old external range cannot be established, hold
affected work and adopt a reviewed new identity namespace/epoch strategy before
new acceptance. Epoch metadata is not permission to resend an uncertain push.

Resume with a reviewed compatibility runbook and designated canary devices only
after outcomes are reconciled or explicitly classified for manual disposition.
Exact duplicates/omissions beyond a nonzero RPO may remain unknowable; record
that uncertainty and business decision. A safe hold/fix-forward is preferable
to claiming transparent zero-loss, exactly-once recovery.

### Alternatives rejected

Resetting offsets to latest loses work; earliest plus rewound DB can resend it.
Randomly changing event IDs or resetting version counters defeats dedup. A
restored active key/session table is not proof that a revoked token is valid.

### Consequences

Recovery/source-epoch schema and signed control provenance are NEW requirements
for the event-contract SPEC. They need cross-ADR review and consumer ownership.
Backup cannot promise zero external duplicates or loss under arbitrary failure.

## A-6. Policy approval, verification and rollout

### Context

Retention, backup cost and business recovery objectives depend on company
requirements not present in this repository.

### Decision

Before SPEC acceptance choose data-class lifetimes/holds, pending-work maximum,
receipt/idem windows, archive/read policy, RPO/RTO, backup/PITR storage/tooling,
key/erasure recovery and owners. Record policy version and evidence; never treat
absence of a retention value as production approval. NEW scripts/jobs/config
are versioned artifacts with dry-run and bounded apply behavior.

Verification includes pending-work purge exclusion, user/device transfer/delete,
idempotency expiry, late receipt/archive reads, interrupted maintenance resume,
backup/WAL gap, encrypted restore, erasure reapplication, revoked-session restore,
consumer-ahead-of-DB, source-epoch mismatch, ID reuse and uncertain FCM accepts.
Test in isolation with approved sanitized data; no real send from restored data.

### Alternatives rejected

An ADR with suggested durations is not policy approval. A down migration is not
a DR drill. A restored database without Kafka/provider/security reconciliation
is not safe service restoration.

### Consequences

Step 14 completes design, not retention or DR readiness. Production gates remain
open until policy and measured restore evidence exist.

## Source and verification cases

Primary reference checked 2026-11-02:
[PostgreSQL 17 continuous archiving/PITR](https://www.postgresql.org/docs/17/continuous-archiving.html).
No backup, purge, restore, key access or provider operation was performed.

| Case | Expected result in companion SPEC |
| --- | --- |
| Old row still has pending work/lease/outbox | Purge excludes it; explicit disposition required |
| Deactivated/transferred recipient | No cascade loss or old-to-new-owner attribution |
| Erased user restored from old backup | Protected erasure/revocation reapplied before exposure |
| Receipt/key near expiry | Accepted lifetime honored except explicit emergency revocation |
| Missing WAL/decryption prerequisite | Recovery gate fails; backup not claimed usable |
| Restored dispatch flag is true | Out-of-band sealed mode still blocks sends/drain |
| Consumer version ahead of restored DB | No regression; reviewed epoch/snapshot reconciliation |
| Old Kafka message references reused numeric ID | Namespace/epoch mismatch blocks execution; ID allocation reviewed |
| Provider accept may precede restore point | No automatic resubmit; uncertainty retained for disposition |
| Restore drill | End-to-end safe-service RPO/RTO measured, not just DB command duration |
'''
