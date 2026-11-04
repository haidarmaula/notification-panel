# M05 replay persistence verification

Date: 2026-11-04. Scope: migration 000025, generated SQLC persistence primitives
and PostgreSQL fixtures. This is implementation slice 1 of the
[operator reprocessing contract](../specs/m05-operator-reprocessing.md), not a
working replay command or production approval.

## Implemented boundary

- Immutable app/request headers and fingerprint/provenance fields.
- App-scoped campaign references, physical-position attempt identity and one
  serializing control row per quarantine position.
- Thirty-second claim leases with database-time eligibility and conditional
  finish/abandon/release operations; stale tokens cannot finalize a successor.
- Immutable finalized attempts and published/prepared/terminal resolutions.
- Initial insertion rejects publication/abandon outcomes. Independent review
  identified this bypass before commit; a regression case covers each outcome.
- Paired down migration refuses retained replay evidence. Existing rollback
  fixtures now remove replay objects before their quarantine parents.

## Evidence and limits

The initial regression failed because the replay request relation did not exist.
PostgreSQL fixtures exercise two racing requests, wrong/unowned tokens, immutable
identities/results, publication-result rollback, expiry/abandonment/successor
fencing, cross-app campaign foreign keys and refusal to roll back retained data.
The full runner also applies all migrations up/down/up on its isolated database.

Full unit/race, build, vet (including tagged integration code), format and the
full PostgreSQL/Kafka integration runner pass. All 25 migrations pass up/down/up.
The runner includes its existing local Firebase SDK HTTP simulation; no real
provider, device or operational credential issuer is involved.

The [request/credential verifier](../specs/m05-replay-credential.md) now supplies
stable selection hashing and authenticated provenance, with unit/race evidence.
[Request registration](m05-replay-registration-verification.md) now adds current
DB authority/revocable grant checks, complete request conflict comparison and
atomic header audit. [Inspection](m05-replay-inspection-verification.md),
[claims](m05-replay-claim-verification.md) and
[execution/recovery](m05-replay-execution-verification.md) now add locally tested
service transactions. CLI/configuration, real issuer integration and production
source/runtime/drain gates remain open. No executable replay path is wired.
