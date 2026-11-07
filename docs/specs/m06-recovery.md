# M06 execution recovery and campaign finalization

The dispatch library recovers a prepared PROCESSING campaign from PostgreSQL
without its original Kafka message or an in-memory reservation. Recovery uses
the existing ledger, marker, lease and outbox; it needs no migration. The
admission worker does not run this library yet.

## Polling contract

1. Call `DiscoverCampaigns` at startup and on subsequent polls. Pass zero to
   start a pass and the last visited notification ID to continue. Wrap at the end
   of a pass. Each read returns at most 100 unowned campaigns from the current,
   unsealed, migration-unheld lineage with a matching preparation marker.
2. `AcquireDiscovered` rechecks those epochs and takes a fresh generation under
   the normal campaign lock. A live competing lease or already completed
   campaign is an ordinary miss. A changed lineage is a hold. Keep the scan
   cursor moving past individual held campaigns; back off on database errors.
3. `RecoverPage` accepts an empty device cursor to start. Follow its returned
   cursor while `More` is true. Each page considers at most 100 devices, has
   a 30-second deadline, and returns acknowledged progress even if a later
   retirement fails. A database error does not permanently fail a delivery.
   Rows that become due behind the cursor are visible on the next pass.
4. `Ready` contains advisory IDs. Reading a page neither reserves an attempt
   nor contacts FCM. Submit these IDs through `Submitter.Send` and persist its
   result through `RecordResult`; both recheck their own authority. A replacement
   worker can retry an abandoned charge only after its saved floor, while budget
   and deadline permit. There is no way to recreate the old send permission.
5. Renew the lease while processing. After recording results, call
   `FinalizeCampaign`; an exhausted page with no ready devices calls it directly.
   Stop and drain in-flight work before releasing a still-active lease. A crash
   after the last delivery commit leaves the campaign discoverable for finalization.

New-dispatch holds suppress ordinary due devices. An expired campaign, exhausted
budget, retry floor at or beyond deadline, or a campaign with no pending devices
can still be closed. A lease acquired under a hold remains bookkeeping-only even
if the hold is lifted. A held, otherwise retryable device may wait for expiry or
hold release before its owner is checked. Runtime seals, migration holds, and
changed origin/source epochs prohibit recovery mutations.

## Durable outcomes

Recovery locks the original user, campaign, installation, binding and delivery
in the same order as reservation. A current-generation unresolved submission can
still record its actual outcome after the deadline. Once a new generation owns
the campaign, it preserves the old charge, identity and floor without inventing
a provider response. An ineligible PENDING device becomes FAILED with the first
applicable reason: `ttl_expired`, `retry_budget_exhausted`, `device_inactive`, or
`token_changed`. The version increment and full update envelope commit together.
A failed or uncertain commit can be inspected/retried without creating a second
version or event. Recovery never refunds an attempt, rebinds a device, or
invalidates an installation.

Finalization rechecks live ownership and compares the ledger size with the
immutable preparation count. Missing rows cannot masquerade as an empty audience.
Pending deliveries and live unresolved attempts (including an early receipt)
leave the campaign PROCESSING. When none remain, an empty audience or any
SENT/DELIVERED/OPENED delivery means COMPLETED. A nonempty, entirely failed
audience means FAILED/`all_deliveries_failed`; late observation flags on FAILED
are not submission successes. Closing the campaign records DB completion time,
clears lease owner/expiry and retains its generation.

## Verification

The opt-in PostgreSQL integration tests cover abandoned claims on both sides of
the budget limit, the retry floor, late acceptance, early receipts, owner changes,
outbox rollback and lost commit acknowledgement. Discovery tests exercise
campaign/device cursor progress, release from a hold, stale epochs and a failed
second retirement followed by a resumed page. Aggregation tests distinguish
empty, partial, entirely failed and inconsistent audiences. Run
`bash scripts/integration.sh` for the isolated regression suite.

On 2026-11-07 all ten recovery/finalization cases passed in the complete local
race suite (PostgreSQL/Kafka integration: 110.663s; FCM: 3.319s), including
the migration up/down/up cycle. Unit race tests, build, vet, formatting and
module verification passed. No production credentials or devices were used.

Periodic delivery-worker composition, deployment policy, consumer acceptance,
and real-device delivery are separate gates. An unknown provider outcome can
still lead to another physical send after the retry floor.
