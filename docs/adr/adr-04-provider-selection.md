# ADR-04 — FCM transport, credentials and registration lifecycle

Application-scope amendment (2026-11-03): [ADR-12](adr-12-multi-application-isolation.md)
requires explicit app scope and per-app policy across this design. kaiapps2 is
one consumer; existing unscoped examples are not a modern cross-app contract.
This is proposed design, not implemented isolation; see CR10 in the
[consistency register](../analysis/adr-01-11-consistency-review.md).

**Status:** proposed, revised 2026-11-02. Design only, not implemented or accepted.
NEW companion SPEC: `docs/specs/adr-04-provider-selection.md` (not written).
See [review](../analysis/adr-04-05-review.md) and
[production plan](../analysis/production-readiness-plan.md).

**Scope:** config/server/worker wiring, notifications provider/factory,
users device-token and mobile sync services/routes, registration schema/query/
repository, API/database documentation, and rollout configuration. This is
broader than the original worker-only proposal because token lifecycle is a
required safety boundary for ADR-01's immutable device intents.


Cross-ADR protocol: [consistency review](../analysis/adr-01-11-consistency-review.md).
Its shared lock/identity rules and verification gates apply to this proposal;
review completion does not mean acceptance or runtime implementation.

## Evidence and confirmed direction

The user confirmed FCM for Android and iOS (APNs through Firebase), roughly
20,000 active devices and retirement of OneSignal. No permanent fallback or
direct APNs backend is required. `cmd/worker/main.go` still constructs
OneSignal; `worker.config.go` requires its credentials; processor labels are
hardcoded ONESIGNAL. No production behavior changes in this document.

Current mobile service maps IOS to provider=APNS and identifies registrations
by push_token. Its update path preserves the old user_id on account switch.
`000002_create_device_tokens.up.sql` has global unique push_token and user
cascade deletion. Thus provider selection alone is insufficient for cutover.

Pinned Firebase Admin Go v4.21.0, messaging/messaging_batch.go, marks SendAll
deprecated, supports SendEach (<=500 messages) and currently creates 50
internal workers. messaging.go supports Fid and marks Token deprecated.
These are findings from the installed dependency, not proof that the existing
mobile SDK supports the preferred identifier. Do not call current SendAll
integration production-ready without validation.

## A-1. Explicit FCM-only production selection

### Context

Silently defaulting to OneSignal contradicts the agreed replacement goal and
can route FCM identifiers to the wrong service.

### Decision

Worker configuration requires PUSH_PROVIDER=fcm for the new production path;
missing/unsupported values fail startup. No auto-detection from opaque address
strings and no selectable mock in deployed config. Keeping the old OneSignal
code during migration is not an implicit runtime fallback. ADR-06 defines
any temporary legacy process/routing, with separate cohorts and credentials.

NEW notifications/provider_factory.go builds the FCM adapter before starting
admission/delivery. ProviderName reports FCM. Production delivery intent and
events use that identity; MOCK is injection-only. Existing historical ONESIGNAL
rows remain historical and are never relabeled FCM by migration.

### Alternatives rejected

Default OneSignal hides missing rollout configuration. Auto-fallback could
send an identifier to an incompatible provider or duplicate an uncertain send.

### Consequences

Update worker docs/config/compose when implemented. Deploy with an explicit
feature gate after modern registration and safe admission are available.

## A-2. Credential source and startup evidence

### Context

SDK construction is not evidence that remote permission, quota or APNs
configuration is correct. Long-lived JSON keys are not the only supported path.

### Decision

Worker requires an explicit FCM_PROJECT_ID and credential mode:
FCM_CREDENTIAL_MODE=adc or file. ADC is preferred where workload identity is
available; file mode uses FCM_CREDENTIALS_FILE mounted read-only from managed
secret storage. Never inline JSON in committed config. Use least privilege
for the target project; no automatic assumption that credential home project
must equal target project (cross-project grants require explicit ops review).
Factory validates mode, accessible configuration and supported SDK init under
a bounded startup context. It must not log raw credentials or their JSON.

Fail-fast on local config/init errors. Authentication refresh, sender mismatch,
APNs credential errors, quota and network health are separately observable.
No real notification is sent as a generic startup/health probe. Deployment
preflight uses designated test devices/dry-run where appropriate and verifies
permissions, quota, iOS APNs setup and both platforms before production enable.

APNs credentials are configured in the Firebase project by its owner, not
mounted into this backend. Only the worker receives Firebase send credentials;
server validates a configured project/app allowlist without send privileges.
Rotation/overlap and rollback procedures belong to ADR-11 and the runbook.

### Alternatives rejected

Only allowing a service-account file prevents keyless deployment. Claiming
SDK init validates remote permissions creates false readiness. Production
credentials must not be copied into tests or browser tooling.

### Consequences

Project ID, app IDs, hosting/identity model and secret owners are still unknown.
These block deployment configuration, not continued design. Constructor and
config signatures change; validate both modes without reading user secrets.

## A-3. Explicit registration identity, version and ownership

### Context

Platform is not address type: an iOS FCM destination is not a raw APNs token.
OneSignal player IDs and existing provider labels cannot prove FCM readiness.

### Decision

Modern enrollment must declare platform ANDROID/IOS, address_type
FCM_FID or FCM_REGISTRATION_TOKEN, opaque address (the legacy push_token
field/column may remain its storage alias), Firebase project/app identity and
installation identity. FID is preferred where the released mobile SDK supports
and registers it; token mode is an explicit compatibility mode, not inferred
by length. A FID is not an authentication credential or a OneSignal player ID.
ADR-06 must verify actual Android/iOS SDK support and enrollment before
selecting cohort address mode; never send an arbitrary app installation ID.

Use existing bearer-authenticated /api/v1/device-tokens registration/update/
delete surfaces with an explicit modern contract extension in SPEC. Old
/mobile/sync body-JWT clients remain a legacy cohort until ADR-06 retirement.
An old request without typed enrollment cannot silently make a record eligible
for FCM. Validate all input before writes; user and device changes are atomic.
Never derive APNS just from platform=IOS. New send eligibility requires active
user, current active binding, verified enrollment cohort, correct project/app,
supported address type and valid registration generation.

NEW installation binding owns a stable opaque installation ID, a hashed
high-entropy installation credential, current authenticated owner and monotonic
version. Also retain a separate owner_epoch: advance it on logout/owner change,
not on a same-owner address refresh. Enrollment returns the credential only on
establishment; refresh or account transfer must prove possession and use
version preconditions. The
installation ID alone is not ownership proof. JWT authenticates the current
user; the installation credential proves control of an existing binding.

For a brand-new binding the allowed registration flow requires a valid user
JWT and modern mobile enrollment; platform/app attestation policy is finalized
in ADR-11. Existing cross-user address conflicts cannot be silently claimed.
If an existing installation secret is lost, require a fresh independently
enrolled address or a reviewed ownership recovery/challenge, never overwrite
another owner's binding based only on knowledge of its address. Legacy rows
need re-enrollment; do not generate a new binding secret for an arbitrary
caller that supplies an already-owned token.

Each address/owner/project generation change creates a **new immutable
device_tokens binding row**, deactivates the old one and advances installation
version in one transaction. A repeated exact refresh only updates last_seen_at
and supplied metadata. Omitted metadata is preserved. Concurrent stale version
returns 409; unauthorized binding operations use the uniform not-found policy.
Logout deactivates only the caller's current generation; a late logout or
invalid-token response must not deactivate its replacement.

The global unique push_token index must be replaced with uniqueness for active
(project, address_type, address identity) and active installation binding.
Inactive historic rows may retain the same address, under retention policy.
All registration entry points, including administrative mutations, share these
invariants; no old route may reactivate unverified records or bypass versioning.

### Alternatives rejected

Blind user_id overwrite lets a caller hijack another binding. Never transferring
ownership strands normal account switching. Installation ID without a secret
is metadata, not authentication. Guessing FCM readiness from a UUID/string is
unsafe. Editing an old delivery's user/address destroys its attribution.

### Consequences

Scope includes migrations and API changes; the previous no-migration claim is
withdrawn. The modern enrollment extension/version rollout is reviewed with
mobile and frontend before the implementation contract is accepted.

## A-4. Retained identity and conditional invalidation

### Context

Old cascade deletion can erase pending intent/history; stale provider outcomes
must not disable a refreshed or transferred registration.

### Decision

Delivery rows reference immutable binding ID/generation plus snapshotted owner,
project/app/address type. Invalidate only the original matching generation.
Future fan-outs exclude inactive bindings; existing intent follows ADR-01's
pre-submit ownership check. Historical SENT/observations remain attributed to
their original owner and binding.

Replace destructive device/user cascade behavior for durable work with retained
tombstones/restrictive FKs and controlled deletion/anonymization. Hard deletion
requires draining/finalizing pending work and the approved ADR-10 retention/PII
workflow; a user-facing device DELETE becomes deactivation with clear API docs.
Retaining identifiers does not authorize indefinite retention of tokens/PII.
Legacy ambiguous rows require inventory and explicit migration decisions.

### Alternatives rejected

Deleting and recreating a mutable row in place can erase evidence or reuse an
identity for a different owner. Blind cascade undermines the durable ledger.

### Consequences

ADR-01's per-device uniqueness is safe because the binding row never changes
owner/address generation. FK/retention detail remains a release-gating SPEC
dependency; no current database behavior is claimed fixed.

## A-5. SDK submission, payload and concurrency contract

### Context

SendEach makes per-message requests with hidden internal concurrency in the
pinned SDK. A batch wrapper alone does not enforce the device rate budget.

### Decision

Replace deprecated SendAll in the implementation milestone. Proposed adapter
uses supported per-message Send calls scheduled by a bounded executor shared
across campaigns, with transport-level limits and retry accounting. Logical
batch size remains bounded (ADR-01); map each response to immutable delivery
identity. Do not adopt SendEach's internal worker fan-out without an explicit
limiter covering all actual requests. Inspect SDK transport retries and bound
total duration; one logical claim may involve multiple physical requests.

Select Message.Fid or Message.Token from declared address_type, never both.
Preserve project/app identity and platform configuration. Use string data
values consistently: notification_id, opaque delivery_id, registration
generation, signed receipt capability and payload schema version. Do not
include user PII, credentials or raw registration addresses in notification
data. Each device gets its own delivery identity/capability, so multicast with
one shared payload is insufficient for this contract.

Client display/tap behavior, TTL remaining, permission state and payload limits
must be verified on real Android/iOS devices. Metadata/SDK init/dry-run alone
does not establish receipt or visible display. SDK retry/error details follow
ADR-01, with sender mismatch/auth distinct from invalid address.

### Alternatives rejected

Calling a deprecated batch API or treating a batch as one request hides quota
and compatibility risks. Notification IDs alone cannot authorize device receipts.

### Consequences

SDK method/identifier availability is version-specific: pin and test the
selected version. No universal SDK capability or throughput is inferred from
the installed package. Mock selection is dependency injection only.

## Open decisions and sources

React Native is confirmed by the user; installed versions/libraries and native
configuration have not been inspected. An existing Firebase project is suspected
through OneSignal, not verified. Use [ADR-06](adr-06-onesignal-fcm-cutover.md)
and its evidence register to inventory/reuse a suitable existing project first.

Before SPEC acceptance: Firebase project/app IDs, mobile SDK/framework versions,
FID versus token cohorts, enrollment/recovery contract, installation secret
storage, JWT issuer/audience and attestation policy, quota and hosting identity.
These require integration facts; do not invent values or ask for raw secrets.

Primary sources checked 2026-11-02:
[Admin setup](https://firebase.google.com/docs/admin/setup),
[Apple setup](https://firebase.google.com/docs/cloud-messaging/ios/get-started),
[send methods](https://firebase.google.com/docs/cloud-messaging/send/admin-sdk),
[registration management](https://firebase.google.com/docs/cloud-messaging/manage-tokens).
Orbit discovery was attempted but DNS resolution was unavailable in this
session; official Firebase docs and pinned SDK source supplied the evidence.
No credential file was read and no provider request was sent.

## Design verification cases for the companion SPEC

| Input / condition | Expected observable result |
| --- | --- |
| Missing/unsupported provider | Startup fails; no implicit OneSignal/mock fallback |
| Valid ADC or file config | Bounded construction; remote permission remains separately verified |
| APNs credential invalid in Firebase | Actionable platform/auth error, no token purge |
| IOS modern FCM enrollment | FCM destination retained; not relabeled raw APNS |
| Legacy OneSignal/unknown record | Not eligible until explicit enrollment; no token guessing |
| Account transfer with valid installation proof/version | Old binding inactive, new immutable row/owner, old intent unchanged |
| Account transfer without proof | Conflict/not-found; existing owner not overwritten |
| Concurrent refresh/logout | Stale version cannot modify replacement binding |
| Invalid-token outcome for old generation | Only old generation invalidated |
| Device/user deletion with outstanding work | No silent cascade loss; controlled lifecycle applies |
| Bounded submission under retry/slow provider | Actual SDK requests obey concurrency/time/rate accounting |
| Different device in same campaign | Different delivery identity/capability, consistent string payload |
