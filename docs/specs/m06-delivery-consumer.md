# Scoped delivery update consumer acceptance

`notification.delivery.updated` carries complete per-device projections. A producer retry can repeat an old record after a newer one reached the same consumer. Step 26 therefore needs evidence at two boundaries: the backend's published bytes and each deployed consumer's durable effects.

## Locally executable contract

The [reference consumer](../../tests/integration/delivery_consumer_contract_test.go) lives exclusively in the opt-in integration suite. Its first input is a real version-2 record from the backend outbox on the disposable Kafka broker. The remaining cases feed valid observation snapshots and deliberately conflicting records to the same parser and PostgreSQL transaction. Its consumer tables live in a disposable test schema.

1. Verify the complete `schema_version=2` envelope and `delivery_<notification_id>` Kafka key. Independently bind the reader's physical log source and topic. Check app, origin epoch, campaign, recipient, delivery and active source epoch against previously approved scope. Check these even on a duplicate. An unknown scope or lineage returns a hold, without claiming an event or advancing the projection.
2. In one database transaction, claim the global `event_id` and the unique `(source_epoch, delivery_id, delivery_version)`. The producer retries immutable bytes, so an exact byte digest identifies an identical replay. Reusing an event ID with different bytes or a version with a different event ID requires reconciliation. Keep claims for previously unseen stale versions too, so a later conflicting copy can still be recognized.
3. This reference consumer accepts a version gap by replacing the business projection with the higher full snapshot. Its durable highest version protects against delayed lower versions. Query the active delivery projection for counts. Kafka record arrivals do not increment delivery totals. A consumer that requires every intermediate transition needs an owner-reviewed reconciliation policy instead.
4. Commit the claim and new projection before advancing the source offset. A failed projection write rolls back the inbox insert. Reopening both the reader and the database connection at the previous offset leaves one durable result. Held records have no successful offset disposition in this fixture.
5. An incoming record cannot activate a restored source epoch. A separate test setup transaction represents an approved manifest: it retains the accepted app/origin/delivery identity, establishes a replacement checkpoint from reconciled facts, and switches the active lineage. The old projection and global dedup history survive this change. Old-source records and relabeled event IDs remain held. No recovery event kind is added to the v2 wire format.

The existing [producer verification](../analysis/m06-update-publication-verification.md) covers ack-before-delete duplication, predecessor blocking and byte stability. The consumer test adds reordered versions 1/3/2, gaps, conflicting IDs/versions, scope checks, persistence failure, restart and replacement-lineage cases. Run it with `bash scripts/integration.sh`.

## Deployment evidence

The repository's worker consumes **send requests**. Its delivery worker and legacy producer publish delivery updates; the repository contains no deployed downstream update processor or authoritative list of its owners. A fixture pass establishes local producer/reference compatibility. Each real consumer owner still needs to verify its own parser, storage and offset behavior before v2 publication on a shared production topic.

- Inventory the actual destination cluster, topic incarnation, partitions, consumer groups and non-group readers, their owners, deployed schema versions and offset policy. Broker group metadata alone cannot identify every direct reader.
- Verify mixed legacy/v2 traffic, the gap policy, event and version conflicts, crash after consumer commit but before offset advancement, and replay of a lower version after a higher one. Confirm the retained inbox and checkpoint history covers the agreed replay window. Deploy compatible readers before enabling v2 publication, or review a separate topic migration with an explicit cutover point.
- For restore or topic recreation, record approved app/origin/source mappings, physical log identity, per-partition boundaries, retained checkpoints and reconciled downstream state. Validate activation, rollback and redaction/tombstone handling with the owners. Test-only scope seeding and manifest activation do not provide production provenance or a disaster-recovery drill.

Decision sources: [ADR-05 delivery events](../adr/adr-05-receipt-ingestion.md), [ADR-07 Kafka topology](../adr/adr-07-kafka-topology.md), [ADR-10 coordinated recovery](../adr/adr-10-retention-recovery.md), and [ADR-12 application isolation](../adr/adr-12-multi-application-isolation.md).
