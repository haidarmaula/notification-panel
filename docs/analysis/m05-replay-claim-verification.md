# M05 replay claim verification

Date: 2026-11-04. Scope: per-position classification, exclusive claim and atomic
attempt audit. Migration 000027 adds an `already_resolved` disposition so a later
operator records an honest no-op without pretending to publish the earlier event.
This is an unwired library, not a working replay CLI or broker executor.

## Implemented

- `ReplayRequests.Claim` verifies full request membership, registers its header,
  then repeats locked current authority/header checks in a second transaction.
  Both transactions have five-second budgets within a ten-second operation bound.
- Registration now checks every selected physical position before reserving the
  UUID: missing evidence and decodable foreign-app envelopes deny the complete
  request without header/audit insertion. Malformed evidence has no trusted app
  attribution and may only receive an authorized scoped refusal.
- Eligibility uses the same assessment as dry run. The claim path creates an
  absent control row after campaign checks and before control/evidence locking;
  the dry-run path still creates nothing. Eligible work gains one random claim
  token and database-time 30-second lease, with started attempt/control/audit in
  one transaction. No campaign, audience, deadline or provider budget is reset.
- Prepared/terminal admission no-ops resolve evidence with matching attempt audit.
  Held/rejected/expired/reconciliation/already-resolved dispositions are durable
  per request/position; they do not obtain an execution proof.
- Existing attempts are returned without reissuing a proof. Only a new claim with
  acknowledged commit returns an internal execution proof. Unknown claim commit
  outcomes return no success; even if that commit persisted, an identical retry
  cannot blindly publish it. Stale-claim cleanup and explicit new request retry
  remain required for abandoned work.

## Review and evidence

Independent review found one scope-order defect: committing a header before
checking a foreign-app position reserved an unauthorized request. The complete
selection scope check now runs inside registration's authority-locked transaction.
A mixed app-A/app-B regression calls both Register and Claim and proves no
header, attempt, control or audit reservation. Final review found no further
concrete lock/FK/idempotency/authority blocker within this claim-only boundary.

PostgreSQL fixtures cover exclusive claim, identical retry without proof,
competing request held without takeover, atomic claim-audit rollback, terminal
resolution, later already-resolved no-op, and actual claim commit followed by
lost acknowledgement. No delivery intents or broker calls are created.

Full unit/race, build, vet (including tagged integration code), format and full
PostgreSQL/Kafka integration pass. All 27 migrations pass up/down/up. Existing
Firebase SDK HTTP simulation remains local; no provider/device or production
issuer is exercised. Hosted CI and deployment are not verified.

## Next boundary

Implement publication-time revalidation against the current credential, grant,
source, acceptance, controls, token and remaining lease. Bound synchronous broker
I/O and commit result/resolution/audit without automatic I/O transaction retry.
Then implement service-owned expired-claim cleanup. The internal claim proof is
not a substitute for those checks; no public execution/wiring exists yet.
