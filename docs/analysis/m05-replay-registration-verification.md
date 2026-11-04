# M05 replay request registration verification

Date: 2026-11-04. Scope: migration 000026, purpose-bound credential provenance,
revocable operational grants and request-header registration. This extends the
[persistence slice](m05-replay-persistence-verification.md); it does not execute
per-position replay, claim evidence or publish messages.

## Implemented

- Operational grants bind application, staff, request, issuer, deployment and the
  complete request fingerprint. Their identity/expiry is immutable; revocation
  cannot be undone. Issuance/revocation tooling remains a restricted-channel gate.
- `ReplayRequests.Register` verifies the pinned step-up credential and configured
  current source. It locks runtime, source, app, staff and operational grant in
  order. Current DB send permission and explicit app grant are mandatory;
  SUPER_ADMIN has no wildcard. App locks serialize app-grant changes and staff
  locks serialize role/status changes.
- The operational grant query checks revocation, scope and credential/grant/MFA
  lifetime using database time. Header registration requires current scoped authority and
  an unsealed deployment with active source. App holds permit bookkeeping for
  historical no-op reconciliation; execution eligibility is a separate guard. It does not inspect campaign
  eligibility or imply that the source manifest has been verified against Kafka.
- Every selected physical position is scope-checked before reserving a header:
  missing rows/decodable foreign apps deny the full batch. Malformed evidence
  can only receive an authorized scoped refusal in the later claim classifier.
- Header and `NOTIFICATION_REPLAY_REQUEST` audit commit atomically. Identical
  retries reauthorize, return the committed identity and add no audit. Changed
  actor/grant/fingerprint/reference conflicts. Audit contains safe provenance,
  position count and incident reference, never raw evidence or credentials.
- Five-second transaction budget, earlier caller cancellation and independent
  one-second rollback cleanup; unknown commit outcomes return no success and
  reconcile by request UUID without automatic transaction retry.

## Review and regression evidence

Independent review found no concrete authority/lock/idempotency blocker in this
slice. Tests observe the real PostgreSQL lock wait during grant revocation,
then prove registration sees the committed revocation. Additional fixtures cover
inactive staff, non-send roles, revoked app/replay grants, SUPER_ADMIN without
an app grant, atomic audit failure, two identical concurrent requests, and a real
commit followed by lost acknowledgement. An idempotent retry still denies a
subsequently revoked grant.

A focused red test additionally exposed MFA freshness with less than five
seconds remaining while JWT expiry was still valid. Verification now retains
the earlier of JWT expiry and MFA expiry, and both verification and the database
query require the operation margin. The regression passes with the race detector.
This fix applies to the unwired verifier; no production exposure is claimed.

Full unit/race, build, vet (including tagged integration code), format and the
full PostgreSQL/Kafka integration suite pass; module verification and pinned
skill integrity also pass. All 26 migrations pass up/down/up.
The later cancellation regression passes in a focused runner with the existing
Firebase SDK HTTP simulation and proves connection release and successful retry.
The reachable vulnerability gate reports zero affected vulnerabilities; one
imported-package and three module findings remain on paths not reached by this
build. Issuer/MFA integration, production broker manifest, real provider/device
delivery and hosted CI are not verified by these fixtures.

## Next boundary

[Dry-run inspection](m05-replay-inspection-verification.md) now provides exact
per-position acceptance/eligibility with no mutations and preserves held-app
no-ops. [Per-position claim transactions](m05-replay-claim-verification.md) now
implement atomic attempt+claim+audit. Next implement
revalidation through bounded broker publication/result commit, and stale-claim
cleanup. Every mutation/publication transaction must repeat current authority;
registration is not a durable authorization bypass. CLI/wiring remain disabled
until those paths and their failure-window tests exist.
