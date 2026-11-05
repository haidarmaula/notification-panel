# M06 fenced results and delivery update outbox

Date: 2026-11-05. Status: implemented and locally tested; worker delivery remains disabled.

Execution.RecordResult accepts only opaque SubmissionResult evidence from its own
acknowledged reservation. Match application, original lineage, campaign owner and
live lease generation, exact attempt ID and claim generation. A result may arrive
after campaign deadline if its lease is still live. Stale results cannot mutate
ledger, installation, policy or outbox. Current app/hold_new changes do not discard
in-flight evidence; sealed/migration-held or changed runtime lineage still fences it.

Use one five-second transaction: runtime -> application -> dispatch policy ->
campaign -> installation -> original binding -> delivery -> update outbox. Lock
installation/binding for update upfront, never upgrade them after the delivery.
Original current ownership is required for invalidation, not for recording a
truthful outcome after logout. No provider or broker I/O occurs in this transaction.

Accepted submission promotes PENDING to SENT and records first confirmed provider
ID/DB acceptance-recorded time. Preserve SENT/DELIVERED/OPENED/FAILED state; an early
observation cannot regress. Confirmed permanent rejection or locally invalid payload fails only a PENDING
claimed device. Only typed UNREGISTERED retires the original still-current binding
and increments installation version without changing owner epoch. A refreshed or
transferred binding remains intact. Credential/APNs/sender configuration failures
also hold new application dispatch atomically; never purge tokens for them.

Transient, uncertain and aborted outcomes resolve the attempt while retaining
PENDING within budget/deadline. Schedule at least max(reserved floor, DB now + 10s,
provider retry hint), or 60s for quota. Round eligible retry times up to database
microsecond precision before checking the deadline. If budget/deadline cannot allow another
attempt, finalize PENDING as FAILED with retry_budget_exhausted or ttl_expired.
Do not refund attempts. Last attempt disposition/reason/fingerprint are internal
operational evidence; retry scheduling alone does not increment delivery_version.

A durable result fingerprint makes repeated persistence of the same exact attempt
idempotent while its lease remains valid, including lost commit acknowledgement.
A conflicting result is held. Replaying persistence never repeats provider I/O.

Publishable fields are effective status, provider ID/acceptance-recorded time,
observed-delivered/opened flags and nullable normalized observation times, failure
time and terminal reason. On a change increment delivery_version and insert one
stable-ID full schema_version=2 envelope in notification_update_outbox in the same
transaction. occurred_at is one sampled DB timestamp, UTC at microsecond precision.
No address, ciphertext, receipt capability, credential or raw provider error appears
in the envelope. Observation flags also reflect effective DELIVERED/OPENED states;
nullable times do not invent missing timestamps. Provider acceptance is not delivery.

Outbox rows pin application/notification/delivery/version, origin/source epochs and
immutable serialized payload. Unique(source_epoch,delivery_id,delivery_version),
restrictive scoped FK and guarded down migration retain evidence. Dispatcher retry
metadata is mutable; immutable event fields are not. The [ordered dispatcher](m06-update-publication.md) preserves
delivery_<notification_id>, stable event bytes/ID, predecessor ordering and
ack-before-delete. Consumer dedup/version acceptance remains required.

Remaining: retry/recovery/finalizer loops, v2 consumer
acceptance, receipts/statistics and delivery composition. No production activation
or external exactly-once guarantee is implied.
