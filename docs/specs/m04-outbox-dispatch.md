# M04 bounded outbox dispatch

Scheduler owns this protocol; HTTP acceptance never publishes. This library is
now participates in a locally tested scheduler composition; executable wiring
and rollout approval remain open.
Retired legacy binaries must not consume it. No provider I/O is involved.

Each invocation has a five-second total context and one transaction. Lock the
deployment control row FOR SHARE first; sealed restore returns held. Deployed
composition uses `NewSourceOutbox` to lock/check the exact active source next,
retaining runtime -> source -> outbox order through publication. A missing,
inactive or mismatched registration returns held without consuming an attempt.
The original `NewOutbox` constructor remains source-unbound for fixtures. Select one
due outbox in the active origin/source lineage, ordered by next_publish_at,id,
FOR UPDATE OF outbox SKIP LOCKED. Read only the project's public identity via
an unlocked join; never lock a parent campaign after the outbox. Foreign keys
retain that project/campaign. Mismatched lineage is retained for coordinated
recovery. Global migration hold permits draining already accepted work.

Publish a version-1 envelope with project UUID, notification ID, original
requested/scheduled times, deadline and persisted origin/source epochs. The
Kafka key remains notification_<id>. No token, proof, staff credential or
mutable provider configuration is in this envelope. Even an expired outbox
is published: admission/recovery owns terminal expiry evidence, not the drain.

Delete the exact locked outbox only after synchronous broker acknowledgement,
then commit. Publish failure (including cancellation/uncertain acceptance)
retains the row and, if the transaction can commit, increments attempts with
saturation and schedules capped exponential backoff with jitter. Persist only
the fixed error code broker_publish_failed. Database/commit failure rolls back
and propagates; this method does not retry a transaction containing broker I/O.
The polling caller must back off on database failure and bound each tick.

Ack before crash/failed commit can republish identical work. This is at-least-once;
M05 must deduplicate the business identity (origin_epoch, notification_id),
validate approved source lineage and match the database acceptance before
creating the complete audience/intent snapshot. No claim of exactly-once delivery
or device receipt follows from broker ack.

The separate modern producer uses synchronous RequireAll, BatchSize=1,
MaxAttempts=1, hash routing, one-second dial/read/write bounds and no automatic
topic creation. Each publish receives at most two seconds of the transaction
budget. A cancelled kafka-go write can still complete later; immutable message
buffers and durable consumer dedup are required. Stop new calls and drain the
producer with Close before source/epoch handoff or restore; caller cancellation
and a released transaction lock do not prove all producer-owned I/O has stopped. Existing legacy producer and
its mapping are preserved. Production activation requires the reviewed
hash-routing/epoch handoff, TLS/SASL configuration, ISR policy and M05 consumer.
Single-broker integration does not establish Kafka HA.

Source: pinned [kafka-go v0.4.51 writer](https://github.com/segmentio/kafka-go/blob/v0.4.51/writer.go)
documents cancellation uncertainty and writer settings. Time bounds here are
local implementation limits, subject to deployment latency verification.
