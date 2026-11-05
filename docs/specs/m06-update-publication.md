# M06 ordered delivery update publication

Date: 2026-11-05. Status: implemented and locally tested; no runtime activation.

UpdateOutbox pins an approved physical-source manifest in the existing source
registry. The trusted publisher must pin that destination, verify live broker
cluster/topic/partition metadata, and own producer shutdown/drain. Each five-second
transaction locks runtime -> source -> one outbox row. Sealed runtime/source
revocation holds publication; migration hold permits already-committed facts to
drain, matching send-outbox semantics. New-dispatch/app holds do not erase facts.

Select one due row in the current origin/source epochs with no retained older
version for that delivery, using an indexed NOT EXISTS predecessor predicate and
FOR UPDATE SKIP LOCKED. A locked/deferred predecessor blocks its successors;
other deliveries remain eligible. Retain mismatched-lineage rows for coordinated
recovery. A due current-lineage successor blocked by a retained foreign-lineage
predecessor returns Held when no other work is eligible. Never silently skip or
delete that predecessor; approved source/restore reconciliation must resolve it. Never lock a campaign/installation after taking the outbox lock.

Decode the bounded closed-schema full v2 projection, requiring all fields including
explicit nullable evidence. Reject duplicates/case aliases/unknown fields/null
required fields/trailing input. Match event ID, app UUID, original user, notification/delivery,
version, epochs and occurred_at to the stored row. A corrupt/mismatched envelope
is retained and deferred with capped backoff; return Held so operators can alert.
This prevents one malformed delivery from continuously starving unrelated rows.

Publish the exact persisted bytes under delivery_<notification_id>. Allow at most
two seconds for the broker call. Delete the exact row only after synchronous ack;
commit afterwards. On failed/uncertain publication, retain it, saturate attempts,
and persist capped exponential backoff with jitter. Never retry the transaction
containing broker I/O. DB/commit failures propagate without claiming completion.

Ack-before-delete failure can duplicate the same event bytes/ID/version. Late
producer-owned I/O after cancellation may also reorder arrival; consumers still
must deduplicate event_id and retain highest version within approved source/delivery
scope. Stop new calls and Close/drain producer before source handoff/restore.
This library creates no goroutines and proves neither exactly-once publication
nor consumer compatibility. Consumer acceptance, live deployment credentials/ACLs,
retry/recovery/finalizer loops and worker composition remain separate gates.
