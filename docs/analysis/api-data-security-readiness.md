# API, data lifecycle and security readiness

The initial November 2 design assessment is preserved below. The current backend checks and owner dependencies are tracked in the [readiness plan](production-readiness-plan.md). Applicable decisions are
[ADR-09 API](../adr/adr-09-api-consistency.md),
[ADR-10 retention/recovery](../adr/adr-10-retention-recovery.md),
[ADR-11 security](../adr/adr-11-security-baseline.md).

## Minimum architectural decisions recorded

- Preserve the API envelope; add safe machine error codes, presence-aware
  PATCH, conditional writes, filter/count consistency and a compatibility map.
- Commit critical mutations/audit together; do not store secrets in audit JSON.
- Separate deactivation/tombstones, PII erasure, archives and backup retention.
- Do not delete pending work/outbox/leases solely because their age exceeds the cutoff.
- Backups/PITR must be recoverable in isolation; restore mode must not
  send push notifications merely because the backed-up dispatch flag is true.
- Source epochs/checkpoints and reconciliation protect consumers/identity
  after DB restore; they do not promise zero external duplicates/loss.
- Browsers must not store shared server API keys; user/session identity and
  operation/object permissions apply separately from network/API-key trust.
- Staff refresh/session revocation, mobile issuer/audience/expiry, key separation,
  binding proof, proxy/origin/resource/rate policy are part of the baseline.

## Initial source assessment on November 2

| Evidence | Meaning / caveat |
| --- | --- |
| pkg/response/response.go | success/message/data are available; Encode errors after headers are not yet handled |
| staff/profile dto.go and segments/helpers.go | Field presence and invalid pagination are not yet consistent |
| notifications/model.go vs SQL constraints | SENDING/notification-SENT constants do not match the runtime vocabulary |
| notifications/routes.go + server wiring + NotificationService.Create/Send | Mutations are wrapped in API key/JWT/audit; no role permission check restricts VIEWER |
| segments/routes.go and service/member methods | Historical pre-M02a gap C21; operation guards are now locally verified in M02a, remaining security gates stay open |
| 000015_seed_roles.up.sql | VIEWER is indeed described as Read only; this is not an enforced policy |
| token/jwt.go and auth/service.go | Existing staff JWT/refresh does not yet have a persistent rotation/revocation family |
| middleware/mobile_jwt.go | HS256/external_id are available; required exp/issuer/audience need stricter enforcement |
| middleware/audit.go | Trusted-peer/XFF guards already exist; default private ranges are too broad to count as an exact production proxy inventory |
| Existing FK/compose/Makefile | Cascades and dev volumes are visible; this does not establish whether company backups exist outside the repo |

Subsequent local work closed the operation bypass, introduced current DB session
families and purpose-specific key rings, and authenticated exact gateway peers.
Staff mutations now lock current authority through the protected write and audit.
The local suites cover cross-project routes, rollback, revocation, grant expiry
and recovery. These findings apply to the mounted backend routes.
[Staff sessions](../specs/staff-sessions.md) · [Gateway](../specs/m02-gateway.md) · [Contracts](../contracts/README.md)

The common response helper encodes JSON before committing its status. Invalid
response data produces the generic JSON 500. If writing the response fails
after commit, the server aborts the response and records the observed status
without exposing payload or transport error details.

Retention now has explicit holds and expiring grants for spent origin nonces,
receipt request markers and terminal send keys. Physical and logical restore
fixtures verify missing WAL, stale sessions, post-backup Kafka ambiguity and
sealing. A read-only erasure preview inventories local dependencies using the
exact project, issuer namespace and subject, and identifies unresolved scope. Corporate identity, business lifetimes, archive read semantics,
consumer erasure, off-host backup custody and access-owner sign-off remain open.
[Retention](../runbooks/retention.md) · [Erasure preview](../runbooks/subject-erasure.md) · [Recovery](../runbooks/physical-pitr.md)

## Outstanding requirement / evidence gates

| Gate | Decision or evidence needed | Before |
| --- | --- | --- |
| A01 contract ownership | Existing caller inventory, OpenAPI/API version map, PATCH/status/auth compatibility | SPEC accepted |
| A02 transactional correctness | Safe error/serialization, list/filter snapshot, audit rollback and CAS tests | Implementation accepted |
| A03 role matrix | Business role matrix confirmed by the user on 2026-11-02; scoped SPEC M02a available. Backend schedule authorization and durable handoff have local evidence. Target privileged-access policy and step-up remain pending | M02a role enforcement locally verified; remaining authorization/security gates are not closed |
| D01 retention matrix | Owner/purpose/lifetime/action for each class; explicit pending-work exclusions | Retention/erasure SPEC accepted |
| D02 RPO/RTO | Allowed lost-data interval and safe-service restoration time, DB/storage failure domains | Backup/restore design accepted |
| D03 recovery proof | Base/WAL/key continuity, sealed restore, erasure/revocation replay, epoch/ID/consumer/provider reconciliation | Production go/no-go |
| D04 archive/history | Coverage semantics, late-report/deletion behavior, checkpoint retention and consumer tombstones | Archive/purge enabled |
| S01 identity perimeter | Gateway/origins/exact trusted proxies, browser/BFF route auth, Laravel claims | Auth SPEC accepted |
| S02 session/privilege | Refresh rotation/reuse policy, logout/offboarding, step-up/MFA/company IdP or controlled operator channel | Production privileged access |
| S03 secret/data custody | Key distribution/encryption/rotation/restore, installation secure storage and receipt window | Production sensitive data |
| S04 enrollment proof | React Native support, app proof versus address-possession recovery/challenge, conflict policy | Modern enrollment enabled |
| S05 bounded abuse | Body/target/hash/request/concurrency budgets, aggregate gateway/local limits and failure mode | Public routes enabled |
| S06 verification | Role/object/claim/refresh/limiter/secret-negative tests, dependency/image/secret checks, restore revocation drill | Production go/no-go |

No company choices have been filled in on the assumption of approval. RPO/RTO and
retention differ from Kafka replication; backups also do not cancel push notifications
already accepted by FCM. A passing scanner does not replace policy sign-off.

## Cross-ADR requirements reviewed at step 15

1. The origin business epoch is separate from the source epoch in both event envelopes and consumer checkpoints, and remains stable on
   normal restarts; activating a recovery snapshot/epoch must not automatically resend.
2. Erasure/tombstone propagation and minimal replay checkpoints require event/schema
   review; this is more than DELETE SQL on hot tables.
3. Shared service permissions cover scheduled_at on create/PATCH; critical audits
   are part of the transaction, rather than best-effort after commit.
4. Browser API-key/cookie/session rollout may require an auth contract version;
   legacy body-JWT sync must not become an enrollment bypass.
5. Revocation/ownership rolled back by restore must be reconciled before
   API/delivery access reopens, separately from postgres restore merely finishing.

The [consistency review](adr-01-11-consistency-review.md) records CR1–CR9 and the external G2–G7 gates. The [readiness plan](production-readiness-plan.md) tracks the subsequent implementation and target evidence.
