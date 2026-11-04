# M05 QUEUED preparation recovery

A bounded database scan recovers accepted work even after its outbox was
published/deleted and its Kafka event quarantined or retained elsewhere. It
discovers metadata without child locks, only for unsealed current-lineage QUEUED
campaigns with original queued/deadline evidence and a due preparation retry.
It never stamps today's epochs onto legacy or restored work.

Each campaign goes through the same atomic Preparer transaction, exact envelope
matching, eligibility/coverage and deadline checks as Kafka admission. No
provider call or Kafka offset/publication occurs. A completed snapshot advances
state and disappears from discovery. Expiry retains unknown audience evidence.
Coverage/migration/project holds persist a deadline-bounded next attempt.
Unsupported targets also persist an explicit bounded hold before returning their
deterministic admission error, preventing one malformed campaign from consuming
every recovery poll. No intent rows are inserted for unsupported targets.

A tick accepts 1–100 metadata candidates and has a 30-second overall budget.
Each candidate commits independently, preserving completed progress on a later
error/cancellation. Counts distinguish known prepared snapshots, terminal
unknown audience, holds and rejected metadata. Transient database errors stop
the tick; the polling caller must back off. The library owns no goroutines.

This slice does not recover PROCESSING leases/provider claims, replay DLQ events,
reconcile old epochs or activate the scheduler. Those remain separate steps.
