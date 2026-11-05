# M06 lease and reservation verification

Date: 2026-11-05. Scope: the first boundary of the
[M06 execution contract](../specs/m06-execution.md). No provider request is enabled.

## Implemented boundary

Migration 000028 and generated SQLC add campaign lease ownership, retained
monotonic generation, latest device-attempt metadata and a recovery index.
`internal/dispatch.Execution` owns a random per-instance identity and explicit
immutable policy. Acquire/renew/release/reserve have five-second transaction
budgets and independent rollback cleanup. Current runtime/app/policy/lineage and
complete preparation are required. Reservation additionally rechecks current
user/installation/binding ownership under the shared parent-first lock order.
Only acknowledged commits return opaque capabilities; no database lookup can
recreate lost permission. The worker remains admission-only.

The latest-attempt fields preserve consumed count and retry floor across lease
takeover. They are not a full provider result history, receipt ledger or update
outbox. Those subsequent M06/M07 requirements remain open.

## Local evidence

- The initial unit test rejected missing types; bounded policy tests now pass
  with the race detector. No implicit policy defaults enable production sending.
- Focused isolated PostgreSQL/race fixtures pass in 10.444 seconds, together with
  the existing Firebase SDK simulation. They cover concurrent lease acquisition,
  single-device charge uniqueness, progress of another device, renewal/release,
  expiry/takeover, persisted floor/budget and stale-generation rejection.
- A transaction started while a lease was live is deliberately held until expiry;
  renew/reserve reject it using guard-time DB clock rather than transaction `now()`.
- A real commit followed by a simulated lost acknowledgement returns no capability.
  The durable lease/charge remains; retry cannot recreate its permission. Constraint
  rejection rolls back the charge. Cancellation leaves the pool reusable.
- Runtime/application/policy/epoch holds, inactive owner, terminal campaign/device,
  future retries, exhausted budget and TTL prevent reservation. Another app cannot
  use the campaign, and another executor cannot use its opaque lease.
- Real same-owner address refresh, logout and owner transfer retire/fence the
  original intent without redirecting it; an unaffected installation still works.
  Tests retain the committed preparation snapshot, without rewriting its audience.
- A strict floor regression observed 9.999999s when two SQL clock reads were used.
  One materialized reservation-clock value fixes the persisted interval while
  retaining fresh live-lease/deadline guards. See ER1 in the
  [review inventory](../../code-review.md).

- The complete isolated suite passes with the race detector in 102.329 seconds,
  including all 28 migration up/down/up checks, retained-evidence down refusal,
  existing authenticated daemon/replay paths and SDK simulation. Migration-order
  fixtures include the new down migration before older schema rollback checks.
- Full unit/race, build and vet (including tagged integration code) pass; focused
  zero-value safety tests also pass. Repeated SQLC generation is byte-stable;
  generated output is retained and module/version files are unchanged.
- Reachable-vulnerability scan reports zero affected call paths. One imported
  package and three uncalled module advisories remain; this is not a claim that
  the dependency graph contains no advisories.

## Remaining M06 work

Receipt signer/payload and [atomic metadata freeze](m06-receipt-metadata-verification.md)
are now locally tested. Explicit app/provider transport routes, bounded FCM
transport/physical retry accounting, rate/breaker permits, single-use
submission, fenced results/conditional binding invalidation, retry/finalization/
recovery, versioned state-update outbox and delivery-worker composition remain.
Provider errors/retry floors are based on the primary sources linked in the SPEC;
local fixtures are not evidence of real FCM/APNs/device delivery or production
capacity. G2/G7 and later release gates are unchanged.
