# M06 update publication verification

Date: 2026-11-05. Scope: predecessor-safe UpdateOutbox and exact-byte Kafka v2
publication. [Contract](../specs/m06-update-publication.md). Runtime remains disabled.

Unit/race tests validate closed complete projection decoding, explicit nullable
fields, duplicate/case/unknown/missing/null-required/trailing/oversized rejection,
and the existing delivery_<notification_id> key. Query changes use existing schema;
`make sqlc` regenerates their code. No additional migration is needed.

Focused PostgreSQL/Kafka/SDK race tests pass in 11.684 seconds. Two drainers cannot
overtake a locked predecessor. A deferred delivery blocks its own later versions
while another delivery progresses. Ack followed by failed delete/rollback republishes
identical bytes/ID/version. Lost commit acknowledgement returns no completion claim;
a committed deletion is not recreated. Source revocation/sealed runtime holds work,
old lineage is retained, and malformed payloads are deferred without publication.
Migration hold permits existing facts to drain, matching the send-outbox policy.

A real Kafka round-trip verifies the exact stored bytes and key before outbox
absence is asserted. The test publisher checks live cluster/topic/partition metadata
through the same producer. The fixture source incarnation initially mismatched its
registered identity and correctly returned Held; the corrected fixture publishes.
No production broker/device/credential is contacted.

Focused review identified UP1: mixed-epoch predecessors needed explicit treatment.
The drainer now returns Held for a due current-lineage successor blocked by older
foreign-lineage evidence. It retains both for coordinated source/restore recovery,
without automatic skip/delete. All other eligible deliveries are considered first.
No additional concrete ordering, ack/delete, fairness or decoder issue was found.

The complete isolated race suite passes in 112.949 seconds for
`hello/tests/integration` and 2.609 seconds for `hello/internal/fcm`, including
mixed-epoch reconciliation and cancellation retaining evidence/releasing locks.
All 30 migration up/down/up checks pass; containers/volumes are removed afterwards.
Simulation records 63 completed FCM and three OAuth requests across the existing
scenarios. Full unit/race, build, integration vet, formatting and module verification
pass. The current vulnerability scan reports zero affected call paths; one
imported-package and three uncalled-module advisories remain.

Consumer ID/version dedup, source handoff and authenticated deployment composition
remain acceptance gates. Cancellation alone cannot stop all producer-owned late
I/O; owner Close/drain is still mandatory before handoff. Retry/recovery/finalizer
loops, receipt/statistics and delivery worker activation remain open.
