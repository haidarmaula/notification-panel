# M06 receipt metadata verification

Date: 2026-11-05. Scope: immutable capability metadata committed with device
reservation. Contract: [receipt/payload](../specs/m06-receipt-payload.md#committed-signing-metadata).
This is not provider execution or production activation.

## Implemented and locally tested

Migration 000029 adds scoped immutable metadata and a restrictive composite
application/delivery FK. Receipt policy is explicit with bounded whole-second
window; there is no default production observation window. Reserve locks the
existing parent/delivery before metadata and freezes version/key/environment/
expiry atomically with the charge. Original scope comes from immutable identities.
The reserved result exposes copied signing material only after acknowledged commit.

Focused PostgreSQL/race and existing SDK simulation pass in 11.627 seconds:

- Lease takeover and key/window rotation retain the original material and exact
  deterministic capability; a new delivery uses the new policy. Missing old keys
  fail signing instead of replacing the capability. Returned value mutation cannot
  change retained material.
- Expiry uses ceiling of the persisted fractional-second deadline plus the explicit
  window, never retry wall-clock time. Foreign environment and a deadline no longer
  covered by the frozen expiry hold without charge or metadata rewrite.
- A real commit with lost acknowledgement leaves metadata and charge durable but
  returns no reservation/material. Failure after metadata insertion rolls back both;
  metadata persistence failure consumes no attempt. Successful retry after rollback
  produces one metadata row and one charge for the eligible device.
- Already charged legacy intents without metadata remain held. Another application's
  delivery ID fails the scoped FK. Newline labels are rejected by SQL constraints.
- Updates/deletes and down with retained metadata fail; metadata remains unchanged.
  Existing execution race/lease/ownership/TTL/budget regressions also pass.

Narrow independent review found no additional actionable lock-order, scope,
uncertainty or overwrite fault. The expiry coverage check does not prove detection
of every arbitrary database deadline mutation; normal accepted-deadline immutability
remains a separate campaign invariant.

The complete isolated suite passes with the race detector in 101.134 seconds,
including all 29 migration up/down/up checks, expiry overflow-range hold, prior
execution/replay/identity behavior and scoped SDK simulation. Full unit/race, build
and vet (including integration code) pass. The vulnerability scan reports zero
affected call paths; one imported-package and three uncalled module advisories
remain. Module/version files are unchanged.

## Remaining work

Configure reviewed key/window policy and retained signing-key overlap before
activation. Route-aware FCM transport/physical request limits, single-use submission,
fenced outcomes/conditional binding invalidation, retry/recovery/finalization,
versioned update outbox and worker composition remain. Receipt evidence/statistics,
real Android/iOS integration and all operational/release gates remain open.
