# M06 acknowledged single-use submission

Date: 2026-11-05. Status: implemented and locally tested; worker delivery stays disabled.
Related: [execution](m06-execution.md), [transport](m06-fcm-transport.md).

A Submitter binds one Execution, trusted FCM client, receipt signer and retained
address protector. Its Send first reads an advisory immutable route and rejects
unsupported routes before charging. It acquires a client permit before Reserve;
claim failure releases the permit neutrally. SubmitReserved supports a caller
that already owns a pre-claim permit and acknowledged reservation. It rejects a
permit from another client. Copies of a reservation share an atomic one-use bit;
consume it before loading/decrypting/signing, and never restore it after failure.
No row lookup, unknown commit or zero-value reservation creates send permission.

Load only an unresolved PENDING exact attempt under the still-live owner/generation,
current origin/source epochs, active app/policy/user, complete preparation and
current unretired original binding/owner epoch. Preserve runtime -> app -> policy ->
user -> campaign -> installation -> binding -> delivery -> receipt metadata order.
Use existing bounded transactions and require commit acknowledgement. No network
I/O runs under their locks. Compare frozen metadata with the reservation; never
freeze replacement signing metadata during pre-send validation.

Decrypt the original binding ciphertext under its application/installation/binding
AAD and retained key version. Build only committed title/body/string-data JSON,
accepted deadline and receipt material; no request-supplied destination/payload.
Payload must be a JSON object of strings (empty object is valid). Reject malformed,
non-string or reserved data rather than dropping fields. Clear plaintext bytes
when finished; SDK strings cannot be guaranteed erased from Go memory. Never log
whole reservations, leases, payloads, requests, ciphertexts or capabilities.

The transport runs a final authorization callback after credential refresh and
physical pacing, before serialization retiming/base RoundTrip. It repeats the
same DB checks and compares the exact prepared title/body/data/deadline/route/
ciphertext/identity snapshot. A changed snapshot is held. An unknown validation
commit makes no provider call. The callback returns a conservative monotonic local
cutoff: time sampled before the transaction plus the DB-observed lesser remaining
lease/deadline duration, capped by lease policy. Transaction/reply latency therefore
cannot extend authority. The physical request context is additionally bounded by
that cutoff; Android TTL is retimed afterwards. Absolute APNs/accepted deadlines
still require reviewed clock synchronization across deployment/provider hosts.

A failed callback produces safe NotSubmitted authorization evidence with zero
physical FCM requests and preserves the charged attempt. Provider acceptance,
rejection and uncertainty are returned with the original reservation in an opaque
result for the later fenced persistence protocol. This boundary does not persist
acceptance, invalidate a binding, refund a charge, retry or publish an update.
Lack of a result remains recoverable from the durable charge after lease takeover.

Row locks cannot fence arbitrary process suspension, future ownership changes or
already accepted provider I/O. A logout/hold after the last check may race a send;
receipt ownership checks and generation/attempt-fenced result writes remain
mandatory. Do not claim exactly-once external delivery or full M06 completion.

Required local verification: copied/concurrent reservations, unknown commit, lost
lease/epochs/holds/ownership/retirement/status after preparation, credential/pacing
delay, immutable payload mismatch, missing signing/decryption key, no locks across
provider I/O, cancellation, and actual SDK/HTTP through the isolated simulation.
