# M06 scoped provider execution

Date: 2026-11-05. Status: lease/reservation persistence and service are implemented
and locally tested; later boundaries remain specified. Delivery runtime remains
disabled. See [verification](../analysis/m06-execution-verification.md).
Dependencies: [ADR-01](../adr/adr-01-provider-retry.md),
[ADR-04](../adr/adr-04-provider-selection.md),
[ADR-05](../adr/adr-05-receipt-ingestion.md), and
[shared lock/identity protocol](../analysis/adr-01-11-consistency-review.md).
This does not accept unresolved product TTL/SLO, credential, mobile, or recovery gates.

## Sequential implementation boundaries

1. Campaign lease and per-device reservation persistence, with acknowledged-commit
   capabilities, DB-clock fencing and ownership checks. No provider calls.
2. Dedicated [receipt signer/payload](m06-receipt-payload.md) and one-device FCM transport, explicit route
   policy, typed outcomes, retry floors, process-wide bounded permits and breaker.
3. Guarded results/recovery/finalization and versioned update outbox; then worker
   composition, simulation/fault tests and operational diagnostics. M06/M07 share
   the receipt/projection contract before any pilot activation.

These are dependencies within M06, not replacements for its completion criteria.
All existing production and per-application activation gates remain mandatory.

## Campaign ownership and reservation

The accepted campaign retains its business identity, deadline and complete audience.
Lease owner is a random per-executor UUID; each acquisition increments a retained
int64 generation. Release clears owner/expiry but never resets generation. No valid
foreign or same-owner lease can be reacquired. Renewal, release and device mutations
match owner, generation and nonexpired DB-clock expiry. Renewal cannot revive an
expired lease. Old capabilities cannot operate after takeover or lineage change.

Acquire only a PROCESSING campaign with complete matching preparation, current
origin/source lineage, an ACTIVE application, approved unheld dispatch policy and
unsealed/unheld runtime. Due PENDING work is actionable; pending work beyond budget
or deadline must also remain discoverable for later no-send finalization. A configured
TTL or accepted deadline is never extended by lease acquisition/renewal/recovery.

Lease policy is explicit and copied at construction: lease duration 30s–5m,
unknown-attempt retry floor 10s–24h, and per-device budget 1–100. These are defensive
implementation bounds, not approved production values. The full executor must
add request/renewal safety validation and calibrated rate/replica allocation before
configuration can enable delivery. Proposed values remain lease 60s, renewal 15s,
request timeout 10s; no process currently enables this path.

A candidate read is advisory and consumes no budget. Before reservation, the future
executor must obtain concurrency, rate and breaker permits and verify its configured
application/provider route. Reservation locks runtime -> application -> policy ->
active user -> campaign -> installation -> immutable binding -> delivery. Discovery
of immutable delivery identity takes no child lock; every identity/current owner is
rechecked under locks. Registry/lifecycle mutations lock application exclusively;
execution holds it for share. FKs do not cause a later acquisition of an earlier
parent lock. Each operation is bounded to five seconds with independent rollback
cleanup, and no automatic transaction retry.

The binding must be current/unretired, its owner and owner epoch must still match
both the installation and intent, and the user must remain active. Same-owner
address refresh does not redirect an old intent to the new binding. Failed eligibility
checks consume no attempt; a subsequent finalizer must terminalize obsolete intents.

Reservation increments one PENDING delivery's attempt count, records a fresh attempt
UUID and current lease generation/time, and persists a minimum next-attempt time.
It rechecks live lease/deadline/due-time/budget in the guarded statement using actual
DB time. An unresolved reservation cannot be charged again in the same generation.
Takeover may reserve it only after its persisted retry floor and while budget/TTL
remain. An attempt is never refunded because a process crashed or lost its response.

Only an acknowledged commit returns a private in-memory attempt capability. A lost
commit acknowledgement returns none, even if the database committed. No row lookup
can recreate that permission. The later executor must consume a shared one-use bit
before provider I/O and recheck deadline/lease/route; a copied result cannot cause
another invocation. This does not fence physical requests under arbitrary process
suspension or provider uncertainty, and does not promise exactly-once push delivery.

## Provider and outcome boundary

Use one logical Firebase Send per device with declared FID or registration-token
address type, per-application project/route allowlist and no OneSignal fallback.
No provider calls occur while holding campaign/installation/delivery row locks.
Never log addresses, credentials, receipt capabilities or raw provider errors.

Provider success means acceptance, not device delivery. Only a typed UNREGISTERED
result may invalidate the original matching binding generation. Generic 400/404,
payload errors, sender mismatch or credentials must not purge registrations.
Unknown outcomes remain charged and retryable only within the persisted budget/TTL.
Auth/APNs/payload/sender failures have distinct safe reason codes and operational
handling. Request rate/concurrency limits must cover SDK physical retries, not
just outer logical calls. Inspect the pinned SDK before composing transport.

[FCM error guidance](https://firebase.google.com/docs/cloud-messaging/error-codes)
and [scaling guidance](https://firebase.google.com/docs/cloud-messaging/scale-fcm),
checked 2026-11-05, require retry backoff and honoring Retry-After. The outer policy
retains at least 10s ordinary retry and 60s quota floors; provider floors are never
shortened by jitter or a configured cap. No call starts at/after deadline. The
final request/SDK transport design must exercise hidden retries against simulation.

Results match exact attempt UUID, owner, generation, lineage and a live lease in
one short campaign-locked transaction. Stale results make no state/outbox changes.
Early receipt evidence is preserved; FAILED remains a submission failure even when
late observations arrive. Published projection changes atomically increment version
and persist an immutable stable-ID full update envelope. Attempt counters and retry
scheduling alone are not state-update events. Uncertain DB/broker/provider windows
must remain recoverable without inventing acceptance or resetting attempts.

## Required verification

- Concurrent acquisition produces one capability; renewal/release cannot revive
  expired or replaced generations, including a transaction begun before expiry.
- Two reservations of one device charge once per unresolved generation; another
  device in the same campaign can progress. Unknown claim commit gives no proof.
- Runtime/app/policy/epoch holds, incomplete preparation, user disable, owner
  transfer, retired binding, terminal state, TTL, due-time and budget deny new claims.
- Takeover preserves consumed attempts and next-attempt floor; stale generations
  cannot mutate results or release a replacement lease.
- Claim rollback leaves no charge. Cancellation/lock waits release connections.
- Simulated SDK responses cover acceptance, invalid token/payload, sender/auth/APNs,
  quota, availability, Retry-After, cancellation and unknown outcomes.
- Result/outbox failure is atomic; recovery covers last-budget crash, TTL expiry,
  missing results and empty/all-terminal campaigns. Out-of-order receipts and
  event versions cannot regress state or double-count statistics.
- Bound memory/goroutines/physical HTTP rate across campaigns and replicas; run
  20k-device load/fault/restore tests before any production claim.
