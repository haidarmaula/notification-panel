# ADR-11 — Identity, authorization, secrets and deployment security

Application-scope amendment (2026-11-03): [ADR-12](adr-12-multi-application-isolation.md)
requires explicit app scope and per-app policy across this design. kaiapps2 is
one consumer; existing unscoped examples are not a modern cross-app contract.
This is proposed design, not implemented isolation; see CR10 in the
[consistency register](../analysis/adr-01-11-consistency-review.md).

**Status:** proposed overall, 2026-11-02. M02a operation guards implemented/local-verified; remaining security decisions and release approval are pending.
NEW companion SPEC: `docs/specs/adr-11-security-baseline.md` (not written).
Gates: [API/data/security readiness](../analysis/api-data-security-readiness.md).

**Scope:** staff/mobile/browser/operator trust boundaries, authentication and
sessions, service/object permissions, request/abuse controls, key lifecycle,
private infrastructure and security verification. No secret is requested/read,
identity provider configured, credential rotated or account changed here.


Cross-ADR protocol: [consistency review](../analysis/adr-01-11-consistency-review.md).
Its shared lock/identity rules and verification gates apply to this proposal;
review completion does not mean acceptance or runtime implementation.

## Evidence and assumptions

Current staff APIs generally require API key + JWT. Staff-management still
has a legacy super-admin gate; M02a now independently checks live DB authority
on notification/segment/member routes and services. C21 is locally remediated;
see [verification](../analysis/m02a-verification.md). Mobile bearer
middleware restricts HS256/external_id but not required issuer/audience/exp;
legacy /mobile/sync has separate body-JWT logic. Staff access/refresh TTLs are
15m/24h; refresh reuse is not a persisted rotating session family.

Audit middleware already checks trusted peers and walks X-Forwarded-For; the
old unconditional-trust description is stale. Defaults trust broad private/
loopback ranges, not a verified company proxy topology. Startup env values,
plaintext dev Kafka and absence of explicit HTTP limits are not production
security. React Native and self-hosted company Kafka are confirmed; SSO/MFA,
host/network/key management and Laravel issuer policy are not known.

## A-1. Trust zones, transport and browser API keys

### Context

A static secret shipped to a browser/mobile bundle is recoverable by its users.
An internal server/network is not automatically an authenticated trust boundary.

### Decision

Separate public staff/mobile API, trusted server integrations, private admin/
metrics, DB/Kafka, provider egress and privileged maintenance. TLS terminates at
an inventoried trusted gateway with protected backend transport; DB/Kafka/admin
use verified TLS/auth where applicable and private network ACLs. No public DB,
Kafka/controller, raw metrics, debug/pprof or unauthenticated operator UI.

The future browser frontend must **not embed shared API_KEY**. Modern staff
browser login is a rate-limited auth entry; authenticated routes require staff
identity/session/permission. A BFF/gateway may inject a server-owned key only
if it independently authenticates/authorizes the caller; it is not a header
laundering public proxy. Direct browser use instead removes/replaces the shared
key requirement for those reviewed routes. Service integrations use their own
scoped identity/credential, not an exposed bundle secret or one global admin.
Existing v1 auth changes need ADR-09's explicit compatibility/version rollout.

Use exact origin allowlists and explicit allowed methods/headers. No wildcard
credentialed CORS or assuming CORS authenticates non-browser callers. Trust
forwarded IP/scheme only from configured actual gateway addresses/CIDRs; strip/
replace client-supplied forwarding headers at that gateway. Do not assume every
RFC1918 peer is a trusted proxy. Default routes cannot bypass the gateway limit/
auth policy through a public backend port.

### Alternatives rejected

Hiding API keys in minified JS is not secrecy. Network location/API key alone
is not user authorization. Broad proxy trust can let an internal peer spoof
audit/rate-limit source identity.

### Consequences

Frontend architecture, origins and gateway topology must be inventoried before
auth SPEC acceptance. Existing dev localhost/plaintext settings stay dev-only.

## A-2. Explicit JWT purpose and identity validation

### Context

A valid signature alone does not distinguish staff access, refresh, mobile
identity or a token intended for a different application/environment.

### Decision

Pin allowed algorithm/key type per trust domain; require configured issuer,
audience, expiration, valid identity and explicit token purpose. Validate
iat/nbf with bounded skew/maximum lifetime, active user/session and current
authority as appropriate. Staff tokens use session ID/jti and key ID from a
configured allowlist. Do not fetch key URLs or select algorithms from arbitrary
untrusted token headers. Separate staff access/refresh, Laravel mobile,
receipt-signing and data-encryption keys/purposes and environments.

Laravel mobile verification accepts only its agreed issuer/audience/algorithm
and nonempty external_id; user IDs in body/path are not an alternative identity.
Factor one modern verified-claims contract for registration/receipt. Legacy
body-JWT /sync may remain only in an explicitly limited migration cohort; it
cannot bypass modern FCM enrollment/ownership checks. Reject missing exp even
if the JWT library would otherwise treat it as optional. Use generic 401 without
disclosing which secret/claim failed.

If asymmetry/JWKS/SSO is adopted, only pinned issuer endpoints, bounded cache/
refresh and fail-closed key selection are allowed. Do not invent an existing
IdP or force immediate Laravel key migration without its owner's agreement.

### Alternatives rejected

Accepting any HMAC variant or shared-secret token from any audience enlarges
trust silently. A role claim from a long-lived token is not current permission.

### Consequences

M02e introduced HS256-only staff access/refresh parsing with required expiry
and positive identity. Migration 000033 binds staff tokens to current sessions.
The protected server now requires the issuer, separate audiences and bounded
key rings with rotation cutoffs. See the [staff session specification](../specs/staff-sessions.md). [M02 assessment](../analysis/m02-baseline-assessment.md) assigns
remaining gates.

Required claim/key-ring config is NEW and per binary; fail startup on incomplete
production policy. Emergency revocation overrides normal key overlap.

## A-3. Staff sessions, passwords and privileged operations

### Context

Reusable refresh tokens and stale role/password sessions complicate offboarding
and compromised-account response.

### Decision

NEW durable staff sessions/refresh families record hashed refresh identity/jti,
expiry, rotation lineage, revocation and session/auth epoch. Rotate refresh on
use atomically; concurrent reuse cannot mint independent valid successors.
Detect reuse and revoke the family under a reviewed client retry policy.
Logout revokes the current session; password/reset/role/deactivation operations
revoke affected authority/sessions as defined, not only future logins. Fail
closed when required session/current-authority lookup is unavailable.

For modern browser sessions keep access token in memory and refresh material
in Secure HttpOnly appropriately SameSite cookies, or a reviewed BFF session.
Cookie-backed refresh/logout/login flows require Origin/CSRF protection and
exact credentialed CORS; SameSite alone is not the whole policy. Never recommend
long-lived refresh in localStorage. Legacy JSON token exchange is migrated
explicitly, not silently combined with insecure cookie defaults.

Retain existing bcrypt verification while introducing hash-version agility;
prefer a reviewed modern password hash for new/rehash credentials with bounded
cost benchmarked on production hardware. Never truncate bcrypt inputs beyond
its supported byte limit. Support long passphrases/managers, bounded Unicode
input, generic login failures and throttling; do not invent accepted corporate
password length/rotation policy. Sensitive local changes verify current/recent
authentication and actor authority; no password/hash in DTO response/audit/log.

Privileged operations require company SSO/MFA/step-up or an explicitly reviewed
restricted operator channel; exact identity provider and local-admin policy
are still open. Bootstrap credentials are one-shot protected setup material,
not a permanent default super-admin login exposed in deployment config.

### Alternatives rejected

JWT expiry alone cannot provide immediate logout/authority revocation. Refresh
rotation without DB serialization allows replay races. Unbounded password hash
work or account lockout can itself become a denial-of-service mechanism.

### Consequences

Session/logout/token contract changes need migration/schema/client tests and
must not resurrect revoked sessions after ADR-10 recovery. No TTL/MFA provider
or password policy is claimed approved solely by this proposal.

## A-4. Deny-by-default operation and object permissions

### Context

Seed labels such as VIEWER do not enforce read-only behavior automatically.
Scheduling on create/PATCH can bypass a permission check placed only on /send.

### Decision

Use current-authority service checks plus route checks. The user confirmed the
business role matrix on 2026-11-02: VIEWER read-only, OPERATOR draft/segment
management, send/schedule only ADMIN/SUPER_ADMIN, staff/role administration only
SUPER_ADMIN. This accepts the scoped policy, not the whole ADR or runtime.
[SPEC M02a](../specs/m02a-staff-operation-permissions.md) and the
[policy contract](../contracts/staff-permissions.json) start notification/segment/
member enforcement; [M02b](../specs/m02b-staff-administration.md) adds existing staff
administration current-authority guards, locally verified 2026-11-03. Operational
step-up remains proposed and separate.

| Operation | VIEWER | OPERATOR | ADMIN | SUPER_ADMIN |
| --- | --- | --- | --- | --- |
| Permitted redacted business reads | Yes | Yes | Yes | Yes |
| Draft/segment operations in allowed scope | No | Yes | Yes | Yes |
| Send/approve/schedule, including schedule fields on create/PATCH | No | No | Yes | Yes |
| Staff/role/security administration | No | No | No | Yes |
| Migration activation/hold, replay and restore | No | No | Separate explicit scoped/step-up operator permission, not role alone | Same explicit operational gate |

Unknown/new roles and operations default deny. Database permission/role changes
take effect through session/current-authority checks; no handler/service method
may infer authorization merely from an active staff row. Preserve SUPER_ADMIN
constraints; actors cannot set their own role/owner through generic patching.

Scheduled approval is privileged/audited delegation at commit, not an ordinary
draft edit. Define service-owned execution of already-approved scheduled work
and emergency hold separately from staff session logout; do not silently claim
offboarding cancels a previously accepted campaign. Policy for withdrawing
not-yet-executed approvals must be explicit if required, using the designed
hold controls rather than inventing a CANCELLED implementation.

Mobile user JWT identifies the owner; installation proof/version and per-device
receipt capability add ADR-04/05's narrower checks. Protected object reads/
mutations/replays use uniform not-found policy where revealing existence is
unnecessary. Service methods enforce it before SQL side effects, including
internal/CLI callers. Legitimate repeated operations still obey documented
idempotency/revocation and do not bypass ownership through dedup.

### Alternatives rejected

A JWT on a route is not permission to mutate. Requeue/restore cannot simply
reuse a user's UI role. Authorizing /send but allowing VIEWER schedule-on-create
leaves the same privilege escalation through a different endpoint.

### Consequences

C21 operation bypass is remediated by M02a route/service and negative API/DB
tests; M02b adds existing staff route/service guards. Atomic mutation/audit,
session controls and remaining
security policies are not thereby implemented or release-approved.

## A-5. Request, resource and abuse limits

### Context

Unbounded bodies, repeated auth and unconstrained provider work can exhaust
API/DB/FCM capacity even without a data exploit.

### Decision

Define per-operation positive body/item/string/depth limits before reading/
decoding. Reject unsupported Content-Type, multiple documents, duplicate keys,
unknown protected fields and invalid identifiers before writes. Example proposed
body caps: auth/receipt 16KiB, ordinary CRUD 64KiB, bounded campaign target input
256KiB; no upload endpoint is implied. Validate target count/payload schema and
Firebase payload limits separately; large broadcast resolves in DB, not a
20,000-token HTTP body. Final production numbers require benchmark/acceptance.

Implementation checkpoint M02d (2026-11-03): [SPEC](../specs/m02d-json-request-limits.md)
uses initial fixed budgets of 64KiB for small JSON operations and 1MiB for
campaign create/update. These supersede the illustrative caps above for current
local code, allowing bounded explicit user-ID lists without assuming short IDs.
They are initial safeguards, not load-validated final limits. Complete body reads
and one-value parsing are enforced; Content-Type, duplicate/unknown protected
fields, per-field/item/depth limits and other A-5 controls remain follow-up.

Set HTTP header/read/write/idle and per-operation DB/provider deadlines; cap
header/query lengths and connection/pool concurrency. Follow ADR-01 rate/lease
and ADR-07 actual SDK/writer semantics. No user-supplied provider endpoint,
credential path or arbitrary outbound URL fetch; allow known provider egress.
SQL values stay parameterized; sort/filter fields are allowlisted.

Use a shared gateway rate policy across API replicas with bounded local defense
in depth: login/refresh, user/installation receipt/enrollment, campaign/send,
and operator paths have separate budgets. Do not let replica count multiply an
assumed global limit. No new Redis/service is assumed; choose reviewed gateway
or bounded shared implementation and protect the DB from limiter write floods.
429 includes Retry-After; fail closed or safely degraded per explicit endpoint
policy when the limiter is unavailable. Bind limits to verified identity/trusted
source where possible, with bounded anonymous buckets; no unbounded metric keys.

### Alternatives rejected

Body size limits alone do not bound JSON/target/hash work. Rate-limiting only
by spoofable X-Forwarded-For can punish the wrong user or be bypassed. Internal
provider concurrency is not bounded merely by campaign worker count.

### Consequences

Boundary tests include malformed/oversized data, slow requests, auth bursts,
replica aggregate limits and limiter failure. No live load/abuse test is run.

## A-6. Secrets, sensitive payload and enrollment evidence

### Context

Registration addresses, installation/receipt material and staff credentials
have different access/lifecycle requirements.

### Decision

Use managed file/identity/key-ring delivery supported by the company environment;
no committed .env/service-account/private keys, inline credentials in commands,
public API config or broad world-readable mounts. Worker alone gets Firebase
send identity; server needs only its own auth/receipt-verification configuration.
Use dedicated random high-entropy keys and purpose/environment separation.

Encrypt sensitive recoverable address snapshots and backups at rest with a
reviewed authenticated-encryption/envelope/key-ID scheme; hash installation
secrets/refresh identifiers where recovery is unnecessary. Key access/rotation/
revocation/restore is independent of DB backup. Never reuse HMAC address lookup
material, receipt-signing key and staff/mobile signing secret. Do not destroy
keys while approved pending work/backup/reporting still needs them unless an
explicit emergency/erasure procedure classifies the consequences.

Receipt capability is not an attestation of physical delivery (ADR-05). Modern
enrollment requires a verified user session plus binding proof for refresh/
transfer; app/installation IDs are not secrets. For new enrollments or lost-proof
recovery choose verified application attestation and/or an address-possession
challenge with bounded nonce/expiry/attempts before trusting an unproven binding.
An App Check-style proof demonstrates an app context, not automatically user
ownership of an arbitrary claimed FCM address. Existing cross-user conflicts
are never silently overwritten. Exact supported React Native proof mechanism
and degraded/offline policy require mobile/security review before production.

Push content is minimal and avoids sensitive user data; client display/deep-link
authorization must use current session/owner after account switch. Logout cannot
retract an OS banner or an in-flight provider request. Do not store capabilities
in analytic events, URLs/query parameters or general logs. ADR-10 controls
retention/erasure of raw material and protects against restore resurrection.

### Alternatives rejected

Encrypting a DB volume while exporting plaintext tokens to logs/backups is not
end-to-end secret handling. App attestation alone is not recipient ownership.
Backend deactivation cannot retract already-accepted third-party delivery.

### Consequences

Key inventory, secret distribution/permissions, payload data classification,
React Native secure storage and enrollment recovery are release gates.

## A-7. Operations, audit and security acceptance

### Context

Maintenance permissions and deployment artifacts can bypass HTTP protections.

### Decision

Use separate least-privilege runtime/admin/provisioning/backup identities,
private TLS Kafka/DB/admin networking, non-root production images and constrained
mounts/resources. Migrations/provisioning/requeue/hold/restore are controlled
audited jobs with environment/cluster guards, dry-run where applicable, scoped
operator identity and no arbitrary shell/SQL/API payload execution. Register new
audit actions without changing persisted constants; minimize fields per ADR-09.

Pin dependencies/images, verify provenance/updates, scan dependencies and images,
check secrets in changes, and validate container/network policy. Tools passing
does not certify the service. Exercise authorization/session/key/limiter failures,
erasure and restored revocations, broker ACLs and negative mobile receipt cases.
Incident procedure includes scoped holds, key/session revocation, evidence
protection and operator communication; no automatic bulk replay as remediation.

### Alternatives rejected

One shared admin credential defeats process isolation. Scanners alone cannot
verify object ownership, schedule privilege or safe cross-system recovery.

### Consequences

Before SPEC acceptance choose company IdP/MFA/gateway, exact origins/proxies,
Laravel claims, role matrix, session compatibility, proof/key management,
numeric resource/abuse budgets and security owners. No universal compliance or
industry certification is claimed. Implementation findings remain open.

## Primary sources and design verification

Checked 2026-11-02:
[JWT best current practices](https://www.rfc-editor.org/rfc/rfc8725.html),
[OWASP REST security](https://cheatsheetseries.owasp.org/cheatsheets/REST_Security_Cheat_Sheet.html),
[authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html),
[password storage](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html),
[secrets management](https://cheatsheetseries.owasp.org/cheatsheets/Secrets_Management_Cheat_Sheet.html).
No credential inspection, penetration test, identity configuration or live
provider/security operation was performed.

| Case | Expected result in companion SPEC |
| --- | --- |
| Browser bundle/network inspector | No server-owned shared API key/refresh secret exposed |
| Wrong algorithm/issuer/audience/purpose or missing exp | Generic 401, no trusted identity |
| Concurrent refresh/reuse/logout | Atomic rotation/revocation, no parallel valid successors |
| VIEWER mutates or schedules through any route/service | Denied with no write/audit side effect |
| OPERATOR draft edit includes scheduled_at | Schedule permission enforced, not mass-assigned |
| Unauthorized object or dedup replay | No data disclosure or ownership bypass |
| Forged forwarded IP from untrusted/private peer | Ignored unless exact gateway policy authorizes peer |
| Oversize/slow/auth flood across replicas | Bounded resource/global rate, safe documented response |
| Lost installation proof/conflicting owner address | No blind reassignment; reviewed proof/recovery flow |
| Rotation/recovery after credential compromise | Revoked keys/sessions not restored by old backup/config |
| Logging/audit/export | No credentials/token/address/capability dump |
| Broker/admin/backup principal attempts excess privilege | ACL/access denial with safe diagnostic evidence |
'''
