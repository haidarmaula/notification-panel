# M02 development baseline assessment

Reviewed 2026-11-09 against current code and local evidence, after M02a–M02e.
This is a development dependency decision, not a production security sign-off.

## Decision

The minimum guard baseline needed to begin M03 SPEC and isolated implementation
is now locally verified: current staff authority, guarded campaign scheduling,
staff administration, safe staff audits, bounded JSON inputs and the scoped staff
JWT parser. M03 may proceed with explicitly recorded integration assumptions.
M02's remaining release controls are open; steps 20/31–33 are not globally complete.
No new enrollment/FCM route may be activated against real users with those
assumptions unresolved. The M02 → M03/M04 dependency means this guard baseline,
not completion of future transaction/session/operations features in advance.

## Criteria and evidence

| Criterion | Assessment | Evidence / limits |
| --- | --- | --- |
| Current operation permissions, including schedule fields | Locally verified | M02a: 14 notification/segment/member routes, independent service guards, denied mutation/DB/Kafka tests. VIEWER can still perform permitted self-profile/device operations; the role matrix is per operation, not a blanket ban on every write. |
| Staff management only current active SUPER_ADMIN | Locally verified | M02b: seven staff routes/services, stale JWT and committed demotion/lifecycle tests. No last-admin or transactional revocation guarantee. |
| No password hashes in new staff audits | Locally verified | M02c safe projections and strict staff payload guard. Production historical cleanup/backups remain unverified; other entity payloads are not certified. |
| Bounded JSON before service work | Locally verified | M02d: 17 handlers, 64KiB/1MiB caps, actual chunked HTTP, 48 rejection route cases. M43 additionally requires JSON media type and identity encoding on staff login, refresh and logout. No depth/field-specific policy claim. |
| Staff JWT purpose/algorithm/expiry/identity | Locally verified after M02e | HS256-only, required exp, positive user_id, correct purpose/secret; malformed-policy tokens rejected before lookup. M33 adds subject, token ID, session binding and current generation checks. The protected server verifies an exact issuer, purpose-specific audience, key ID, expiry and maximum lifetime. Immutable rolling key snapshots support explicit verification cutoffs and immediate removal. |
| Inactive/deleted staff denied | Locally verified in guarded routes | JWT active-status checks plus current-authority checks; M02a/b committed/lifecycle tests. Migration 000033 now also checks family expiry/revocation and account/recovery generations. |
| Unknown 500 errors are generic | Reviewed current branches; scoped regression coverage | Feature-handler 500 branches inspected use literal internal server error. M02a/b tests cover authz errors; not a proof that every mapped 4xx, log or DTO is redacted. |
| Current role on refresh | Locally verified existing behavior | M02e real-DB test uses staff ID 700001, DB role ADMIN and stale VIEWER claim; returned access has current ADMIN. C2's old finding no longer matches source. |
| API compatibility documented | Scoped only | M02a–e contracts record changed permissions, audit and JSON/token rejection behavior. No full OpenAPI/consumer/mobile acceptance yet. |

Evidence: [M02a](m02a-verification.md), [M02b](m02b-verification.md),
[M02c](m02c-verification.md), [M02d](m02d-verification.md), [M02e](m02e-verification.md).

## Remaining control ownership and gates

These are scheduled requirements, not silently waived defects. Changes that
introduce a new sensitive path must implement its applicable controls then.

| Remaining requirement | Owning work | Required before |
| --- | --- | --- |
| Production issuer/audience assignment, key custody, session client cutover and privileged step-up | Remaining M02 identity/session SPEC; corporate auth policy G5 | Production staff API/security sign-off. The [session cutover](../specs/staff-sessions.md) requires fresh login and clients that replace both credentials on rotation. |
| Laravel/mobile verified-claims contract, installation proof, immutable binding, account switch/logout and closing legacy sync bypass | M03, G3/G5 | New enrollment activation and later dispatch/receipt ownership acceptance |
| Content-Type, protected/duplicate fields and field/item/depth limits | Remaining API baseline plus owning M03/M04/M07 DTO contracts | Activation of the affected new/changed endpoints; full request security review before release |
| Target count and provider payload limits | M04/M06 | Campaign acceptance/execution release; request byte caps alone are insufficient |
| Auth/endpoint abuse limits, aggregate bounds, HTTP timeouts, exact trusted proxy policy, secrets validation | Local server limits and gateway checks are implemented; deployment values and fleet policy remain M08/target work | Any production exposure; a single-process limiter must not be described as fleet-wide protection |
| Atomic current-authority mutation/audit and transactional failure windows | Owning M03/M04 transactions using CR1 lock order | Acceptance of those mutations and pipeline crash/recovery tests |
| Historical audit sanitization, restricted copies, retention/restore | M02c operator gate and M09 recovery/retention | A claim of existing-data remediation and restored-data exposure |
| Real devices, numeric SLO, multi-host Kafka, load/fault/restore/pilot | G2–G7, M08–M10 | Production go/no-go; local fixture tests are insufficient |

Current mobile verification is split: device-token middleware pins HS256 but
still does not require exp/issuer/audience; legacy body-JWT sync accepts HMAC
variants and also lacks required exp/issuer/audience. M02e changes neither path.
Audit IP extraction checks peer trust. The environment-based development server
still uses broad private CIDRs; protected deployment ingress instead checks the
configured exact gateway peers, and the receipt policy accepts explicit trusted
TLS proxy addresses. Actual addresses/topology remain an operator requirement.
The business HTTP server sets a 32 KiB header limit, 5-second header-read
timeout, 30-second read/write timeouts and a 60-second idle timeout. The gateway
enforces bounded per-IP/concurrent traffic and per-IP plus aggregate auth
budgets; those in-process limits do not establish fleet-wide or account-level
protection. These source findings explain the remaining gates; they are not
production security sign-off.

## Concrete next sequence

1. Establish the generic multi-application boundary first: [ADR-12](../adr/adr-12-multi-application-isolation.md)
   and [M03a–d plan](../specs/m03a-application-scope-plan.md). Registry/grants and
   scoped queries/legacy route gate precede app-aware installation/binding work.
2. Write the generic scoped installation/binding/enrollment and mobile JWT/proof
   contract, then validate kaiapps2 as one consumer. Each app has its own trusted
   identity policy; Laravel is not a universal dependency. List unverified issuer facts.
3. Implement the agreed backend schema/repositories/transactions and focused race
   tests, preserving immutable ownership/epochs and the CR1 lock order. Keep
   unresolved integration paths disabled/test-only until their gates are settled.
4. Continue M04 durable acceptance, M05 admission, then M06 FCM execution (step 21
   is implemented with that dependency context, not by swapping the current worker).
5. Close the remaining identity/abuse/runtime controls with their owning slices;
   keep the release register open until actual evidence is available.

The user authorized `/Users/koperasiastra/projects/kaiapps2`, branch
`feat/notifications`, on 2026-11-03. Read-only Git inventory verified that the
current branch matches, the working tree was clean and its head was
`3f35661d16b076455011ba316a3be69ede9b6668`. This assessment has not yet inspected
its application code or verified Firebase/APNs/Laravel configuration. Path access
resolves repository discovery, not G3 integration acceptance.

Scope update 2026-11-03: existing M02 grants/tests are operation-level, not app
membership isolation. ADR-12 requires new application guards and A/B evidence.
The confirmed multi-app requirement does not make current runtime multi-app safe.
