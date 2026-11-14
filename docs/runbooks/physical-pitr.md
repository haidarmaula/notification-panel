# PostgreSQL physical backup and point-in-time recovery drill

For host installation and required fixture tools, see [machine setup](../setup.md)
and [operational prerequisites](../tooling/development-tools.md#operational-drill-prerequisites).

Run the following from the repository root with Python 3 and a local Docker Unix
socket. The PostgreSQL 17 image pinned in the runner must already exist locally.
All records, storage volumes and encryption keys belong to this one synthetic
run. Each container uses `--network none`, publishes zero ports, and connects to
PostgreSQL only through a container-local Unix socket. No application, provider,
Kafka client, deployment credential or existing database participates.

```bash
python3 scripts/pitr-drill.py
```

## Recovery sequence and failure evidence

1. Initialize a fresh checksummed PostgreSQL cluster with WAL archiving. Apply
   the repository's migrations and seed two real staff-session families and a
   campaign. Start with both database runtime gates open so the external network
   fence is tested against a restored permissive flag. The fixture uses 1 MiB
   WAL segments to exercise segment boundaries with a small amount of data.
2. Run `pg_basebackup --wal-method=none --manifest-checksums=SHA256`. Commit a
   second campaign and its scoped send-outbox row **after** the base backup.
   Create a named PostgreSQL restore point and record its LSN, segment, timeline,
   system identifier, migration version, base manifest hash, session epoch and
   original outbox digest in a read-only file outside the database. The runner
   pins its SHA-256 and rechecks the file before recovery. An out-of-band
   digest prevents the restored database from choosing its own checkpoint.
3. Beyond that point, commit a third campaign, log out one staff family and
   disable its staff account. Capture the committed account epoch, family,
   reason and effective revocation time in a separately pinned synthetic
   security ledger. Switch WAL, verify the restore-point segment archived and
   stop the source. `pg_verifybackup` checks the base manifest and the archived
   WAL required by the base backup. Actual server replay below verifies the
   longer WAL chain through the selected recovery point.
4. Package the base backup and archive as an OpenSSL CMS AES-256-GCM encrypted
   envelope under a disposable RSA recipient. Store its private key on a separate
   volume. With the ciphertext mounted read-only, verify rejection of an
   unrelated private key, verify the independent ciphertext and manifest
   digests, decrypt to private temporary storage and repeat `pg_verifybackup`.
   Recovery targets can read the decrypted archive; they cannot overwrite it
   or access the recovery private key.
5. Start an independent target after omitting the actual restore-point WAL
   segment from a separate copy of the archive. Require server exit failure,
   the missing filename, PostgreSQL's failure to reach the specified target
   and refusal of a SQL connection. Keep that target isolated. Use a fresh data
   volume for the successful attempt.
6. Start the second target with the complete archive, a named recovery target,
   `recovery_target_action=pause` and read-only transactions by default. Require
   paused recovery at the precise LSN and matching PostgreSQL system and
   timeline. Verify both earlier business IDs, the accepted row's epochs and
   outbox digest. The third campaign must be absent. The recovered authorization
   predicate from the repository must demonstrate that both preexisting
   families appear active again while external access remains blocked.
7. Complete the isolated recovery. In one administrator transaction set
   `sealed=true` and `migration_hold=true`, reapply the authoritative account
   disable and family revocation, and rotate the singleton staff session epoch.
   Verify matching revocation time and account generation, and run the
   application's `StaffSessionActive` query for both families. Even the family
   without an individual logout must be denied. Recheck the scoped outbox and
   the absent later campaign. The data plane stays offline through teardown.

## Recorded synthetic result, 2026-11-07 UTC

PostgreSQL 17.10 applied migrations through 000034. The source system ID was
`7705492332403277865`, timeline 1. Recovery paused at `0/D58C10` using segment
`00000001000000000000000D`. The independent base manifest SHA-256 was
`f1a8e3def2ae1bbecb6bfb69dba4255f64ec3adf06f8d3d3e773a0c1fc7c202c`.
The restore point was 0.989 seconds old when the source stopped. Exactly one
later fixture campaign fell outside it. The base backup took 1.434 seconds,
decrypt and verify took 0.659 seconds, and starting the complete target through
sealed business and session verification took 2.338 seconds. The full wrapper,
including the recovery failures and resource cleanup, exited 0 in 10.571 seconds.

## External recovery gates

- The **database and storage owners** identify the actual base/WAL archive
  repository, independent failure domains, immutable retention settings, restore
  permissions, supported versions, data checksums and archive capacity. Verify
  continuous WAL archival, last usable restore point and gap/freshness alerting.
  A read-only mount and independently pinned digest on one Docker host exercise
  local integrity. Production requires independent, access-controlled and
  tamper-resistant custody for checkpoints, archives and recovery evidence.
- The **security and key owners** approve encryption/key IDs, separation of
  custody, historical key availability, rotation, audit and the independent
  erasure/revocation ledger. Exercise recovery with the real custody process in
  an approved isolated environment. The fixture encrypts one captured package.
  Its live WAL archive and temporary plaintext volumes stay on the test host.
  Production needs encryption and access control during archival, transport and
  recovery as required by the approved storage and key policy.
- The **incident, platform and consumer owners** fence prior writers and retain
  evidence for old Kafka source IDs, consumer offsets, event versions, external
  sends and database ID high-water marks. Resolve uncertain mobile ownership,
  erasure, MFA, key and account decisions against authoritative records.
  Authorize a new source epoch, canary scope, rollback procedure and any
  network or service enablement only after cross-system reconciliation.
- **Business and operations owners** set and approve numeric RPO, RTO and PITR
  window. Measure actual point age from off-host retrieval and end-to-end time
  through safe service resumption and validated canaries. This drill ends with
  dispatch sealed. It supplies local timings for a small synthetic database.

The independent logical restore exercises Kafka and outbox reconciliation in
more detail. [Sealed logical restore](sealed-restore.md) · [Recovery ADR](../adr/adr-10-retention-recovery.md)
