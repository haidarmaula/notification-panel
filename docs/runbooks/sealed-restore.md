# Sealed restore and rollback drill

## Run the disposable drill

From the repository root, run the following with the prerequisites for the
integration fixture installed. The command takes no arguments. It builds a Linux
probe using the repository's pinned dependencies, then starts the normal
PostgreSQL, Kafka and simulated provider integration suite. The integration
runner owns the fixed Docker project and port lock for its entire lifetime.

```bash
GOFLAGS=-race GOCACHE=/tmp/hello-go-review-cache bash scripts/restore-drill.sh
```

Look for `TestSealedBackupRestoreDrill`, the `Sealed restore passed` line and a
successful suite exit. The line records the SHA-256 and size of the synthetic
logical backup, the backup and restore durations, elapsed recovery time through
validation, and the real log-end offsets of partition 0. Fixture creation and
compilation occur before the timer. The elapsed times characterize this fixture
on the machine where the command ran.

1. The test prepares one campaign with two devices, records a provider
   acknowledgement and an authenticated OPENED report, and captures counts and
   SHA-256 digests over the selected tables. The snapshot covers installation
   ownership, protected binding data, user reads, request deduplication, receipt
   signing metadata, the attempt budget, stable delivery versions and both
   immutable outbox events.
2. The pinned PostgreSQL container runs `pg_dump` against the single synthetic
   schema. The source then publishes its first event to the fixture Kafka
   partition and inserts a second campaign. The test reads the broker event at
   its actual offset and checks the stable event ID and source epoch.
3. A fresh container starts with the running fixture's immutable image ID,
   `--pull=never`, `--network none` and a tmpfs data directory. It has no host
   port, broker or provider route, worker process, credentials from deployment
   or production consumer group. A single PostgreSQL transaction restores the
   archived fixture schema, selects it for probe connections, sets `sealed=true` and `migration_hold=true`,
   and commits. The host holds the synthetic dump in memory. Probe access uses
   the local PostgreSQL socket inside that container.
4. The probe compares the captured table counts and digests and checks the
   individual campaign, attempt, provider acceptance, OPENED state, versions
   and first event ID. It finds the already published event pending in the
   restored outbox. It verifies the restored sequence is below the externally
   known post-backup campaign ID. These are two concrete reconciliation cases.
5. Sealed runtime startup, preparation and publication guards hold. Inside one
   uncommitted transaction, the probe proposes a different source epoch under
   the same origin, validates the original physical source registration, then
   presents the original admission event and attempts an outbox drain. The
   preparation guard rejects the stale source and the outbox reports idle with
   every old event still present. The
   probe rolls back the candidate source change. The seal, original provenance
   and evidence digests must match again. For the rollback trial a transaction temporarily gives the fixture schema its
   production name, `public`. The actual receipt migration down script must
   refuse rollback with retained evidence (SQLSTATE 23514). The trial rolls back.
6. The outer harness checks the Kafka log end stayed fixed throughout restore
   and validation. Cleanup removes the target container and tmpfs volume and
   the fixture runner tears down its sources. Do not feed an external DSN, dump,
   broker, group ID or deployment keys into this exercise.

## Local measurement

On 2026-11-08, the race-enabled fixture restored a 170,804-byte logical dump
with SHA-256 `ce2c463f942aea9952e4aba6521ab9646bcdb07d26d4e45d05e0ccb5889c0101`.
Backup took 265 ms, isolated database creation and restore took 1.793 s, and
recovery through the final canaries took 2.006 s from target creation. Total
elapsed time from backup start was 2.438 s. Partition 0 advanced from log end
0 to 1 only on the source. The restored outbox still held two original events.
The 14 table comparisons and rollback/lineage probes passed.

## Recovery decision record for a deployment

The incident owner timestamps the last confirmed recoverable transaction and
the incident, records the recovery point selected, and measures recovery
through safe, reconciled service resumption. The business and platform owners
must agree numeric RPO (maximum data loss), RTO (maximum recovery time),
retention window, failure domains and criteria for accepting ambiguous sends.
Record the actual achieved RPO and RTO for each staging or disaster recovery
exercise, including all reconciliation and canary time.

- **Backup and access.** The database owner identifies encrypted base backups
  and continuous WAL archives in an independent failure domain, verifies
  checksums, timeline, PostgreSQL/system identity and WAL continuity through
  the target timestamp, and restores to a new isolated cluster. Record the
  backup manifest ID, migration version, applicable encryption/signing key IDs,
  restore permissions and independent key-recovery evidence. Restrict archive
  access, monitor gaps and freshness, and test recovery of the required historical
  keys with their owners. Recovery records contain identifiers and measurements
  only. The local logical backup covers a single committed snapshot. A separate
  fixture exercises physical base backups, encrypted archived WAL, and sealed
  session reconciliation. Target PITR, off-host retrieval, archival retention
  and key recovery still require deployment-specific tests.
  [Physical PITR drill](physical-pitr.md)
- **Containment.** Platform and security owners fence the former writer, disable
  scheduler, admission, replay and outbox drain, and deny provider and broker
  egress outside the restored database before restoring any bytes. Record
  process exits, completed producer drains, disabled group membership and
  network policy. Keep the restore sealed and restrict administrative mutations.
  Apply current staff/session, MFA grant, mobile ownership, key-revocation and
  data-erasure records from their independent authorities before evaluating
  access. Hold scopes with uncertain ownership.
- **Kafka and sends.** Broker and consumer owners capture cluster identity,
  approved topic incarnation, partition count, log start/end, group committed
  offsets and external projection checkpoints. Map each retained business
  origin, source epoch, physical log-source ID, event/version, attempt, lease and
  deadline to observed consumer/provider evidence. Classify missing history,
  ambiguous physical sends and reused ID ranges explicitly. Preserve old outbox
  identities and charged attempts. Use externally verified ID high-water marks
  before accepting new IDs, or retain the hold pending a reviewed namespace
  transition. Document separate disposition for all uncertain sends.
- **Approval and rollback.** Obtain a named incident, database, security, broker
  and application/consumer-owner sign-off on the reconciliation manifest,
  recovered key policy, rollback-compatible binary/schema versions, canary scope
  and cutover time. The incident owner activates the reviewed lineage only after
  those approvals. Restore service gradually on designated test devices and
  verify counts, stable IDs, consumer checkpoints, errors, duplicate suppression
  and timeout handling. If a canary or restore invariant fails, stop new work,
  drain in-flight producers, block egress, retain both lineages and reconcile
  external side effects before reverting compatible processes. Preserve retained
  receipt/outbox evidence when a migration refuses its down step.

See the [data lifecycle and recovery ADR](../adr/adr-10-retention-recovery.md),
[Kafka recovery boundaries](../adr/adr-07-kafka-topology.md) and
[operator replay runbook](operator-replay.md) for the corresponding controls.
