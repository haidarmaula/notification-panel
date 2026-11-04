# M05 serial admission coordinator

The coordinator owns one fetched message until PostgreSQL preparation or durable
quarantine succeeds and its assignment-bound acknowledgement succeeds. It never
calls a provider, launches a per-message goroutine or fetches a later message
while evidence is unsafe. This is a library; the Kafka reader adapter, actual
rebalance/generation verification and process wiring remain separate gates.

Reader.VerifySource must validate deployment approval and the configured physical log identity before
fetching. Every fetched delivery carries an acknowledgement bound to that exact
assignment/generation. Assignment loss must fail acknowledgement and stop this
run; a new assignment redelivers and database preparation deduplicates it. A
plain unconstrained commit callback is not an approved production adapter.

Decode the strict modern envelope, then prepare without network I/O. Deterministic
envelope/target/reconciliation errors are quarantined immediately. Other errors,
including a preparation hold or panic, retry at most three times in a 90-second
admission budget, with at most 30 seconds per call and capped jittered delay.
Shutdown cancellation skips poison classification and acknowledgement. A panic
is sanitized; no panic value is retained as an error message.

After bounded admission exhaustion, store the raw source evidence and fixed
reason. If storage fails, retry that same evidence indefinitely with five-second
calls and capped delay until success or shutdown. No new fetch or acknowledgement
is allowed in that interval. Quarantine success permits committing this source
message; it does not mark the campaign FAILED. QUEUED recovery remains required.

Acknowledgement has a five-second call bound. Any failure ends the run without
fetching another message; prepared work/evidence remains durable for redelivery.
The caller owns reader lifecycle, backoff/recreation and source verification.
The coordinator has no detached goroutines, logging of raw bytes, or automatic
activation. Exact kafka-go rebalance/shutdown behavior must be tested in its
adapter before replacing the retired worker binary.
