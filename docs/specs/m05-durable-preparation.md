# M05 atomic preparation

This slice prepares a modern accepted campaign in PostgreSQL. It performs no
Kafka offset commit, provider call, payload publication or capability signing.
Serial Kafka consumption, source-aware quarantine and recovery scans remain
separate M05 slices. The dispatcher and consumer binaries remain unwired.

Prepare validates the version-1 envelope, then uses a bounded 30-second
REPEATABLE READ transaction. Lock deployment control FOR SHARE, project control,
dispatch policy, then campaign. Recheck exact project, origin/source epochs,
requested/scheduled times and deadline against the accepted database row.
Missing epochs, DRAFT/SCHEDULED state and mismatched envelopes fail closed.
Sealed restore prevents preparation. Current lineage is required even for
duplicate events; normal restart does not rotate it.

An existing complete preparation returns the same immutable counts without
re-resolving users or creating new intents. A valid terminal acceptance is a
no-op. PROCESSING without a modern complete marker is an explicit reconciliation
error, never proof of completion. Expired QUEUED becomes FAILED/ttl_expired with
no snapshot; unknown audience is not an empty audience.

For a live QUEUED acceptance, global/project hold, missing policy, non-ACTIVE
project or insufficient modern coverage commits a visible bounded preparation
hold and next_attempt_at under the original deadline. A hold is an admission
error, so callers cannot acknowledge it as a complete snapshot.

Resolve a set of project-scoped users from supported BROADCAST, SEGMENT and
INDIVIDUAL targets. Record resolved users before status filtering, eligible
ACTIVE users and eligible active binding generations separately. Overlapping
targets and segment memberships cannot duplicate users or bindings. Unsupported,
unassigned or empty target sets are errors. Eligibility uses the same transaction
snapshot for coverage, counts and set-wise intent insertion. Every eligible
user needs a current binding and no active legacy token; no partial snapshot
is committed when coverage fails.

Each intent retains an opaque delivery UUID, project/campaign, binding generation,
installation, original owner/epoch, version zero and untouched attempt budget.
Composite foreign keys bind this attribution to the immutable binding. Raw
addresses and proofs are not duplicated. Complete preparation records counts
and the original acceptance epochs. Intents, complete marker, counts and
QUEUED→PROCESSING commit together. A genuine empty snapshot instead completes
the campaign with known zero counts. A later enrollment never extends a completed
snapshot; M06 must recheck current owner/eligibility before each submission.

Retry only whole database transactions on SQLSTATE 40001/40P01, at most three
attempts within the original timeout. There is no network I/O to repeat.
Unknown commit outcomes propagate; repeating the same event discovers committed
preparation. Down refuses to discard modern intents, complete markers or hold
evidence. Historical legacy delivery rows remain separate and never establish
modern completion.

These local bounds are implementation limits, not approved latency/SLO values.
The 20k-device preparation benchmark and production identity/policy gates remain
required before launch. PostgreSQL source: [repeatable read](https://www.postgresql.org/docs/17/transaction-iso.html#XACT-REPEATABLE-READ).

Local evidence: TestAtomicPreparationTwentyThousandDevices inserts 20k distinct
users with one current binding each in an isolated PostgreSQL 17.10 schema.
Observed preparation transaction: 4.03 seconds on the developer Docker fixture;
all 20k intents and exact denominators committed. Enrollment/encryption use
synthetic preseeded records, and Kafka/FCM are outside the measurement. This is
a bounded functional scale check, not a staging load/burst/soak acceptance.
