# ADR-12 — Multi-application notification backend

Date: 2026-11-03. Status: **proposed architecture; not implemented**.
The user confirmed that this backend must serve multiple mobile applications.
That requirement is accepted; schema/API/grant details below are the proposed
baseline, not proof of runtime isolation or wholesale ADR acceptance.

## Context and boundary

Current users.external_id and device_tokens.push_token are globally unique;
campaigns/segments have no application key. M02 guards enforce staff operations,
not application membership. A second application cannot safely be activated by
adding another Firebase credential to this runtime.

One deployment serves multiple logical applications within the organization.
An application may have Android and iOS clients; this is not a new application
per OS, physical device, SDK instance or business user. React Native, Flutter
and native clients implement the same backend contract. kaiapps2 is an initial
consumer/integration example, not the platform's identity or protocol template.
Multi-organization SaaS tenancy and cross-application campaign orchestration are
not added to v1.

Production/staging use separate deployment identities, databases, Kafka topics/
ACLs and secrets/provider environment policy. A request cannot select or override
the server environment. App IDs are resolved within that deployment. Shared
production/staging tables with an optional environment header are not the baseline.

## Application registry and principals

Introduce an immutable application identity and explicit active/held status.
Renaming a display name never reassigns historical ownership; retirement keeps
required identity/FK/recovery floors. Registry records refer to reviewed provider
routes and versioned mobile authentication policies. Store secret references,
not private key JSON, in control-plane metadata; provider send credentials stay
worker-only. Runtime configuration snapshots remain immutable; configuration
rotation uses a reviewed version/refresh mechanism, not mutation of shared config.

Public app IDs, package/bundle IDs, app headers and request fields are routing
selectors, not credentials. Resolve the selected app, authenticate the issuer/
audience/purpose under its configured policy, then establish an app-scoped
principal. Never trust a client-selected issuer/JWKS URL or accept a token under
app A's key as authority for app B merely because its subject matches.

Mobile identity is (app_id, trusted identity-policy namespace, external subject).
The same external_id in two apps is two notification users by default. Shared
SSO is allowed through explicit per-app trust policies; it does not silently
merge profiles, devices or audiences. An app-specific subject/client binding or
verified app-origin mechanism must distinguish apps when upstream tokens alone
do not do so. Cross-app account linking is a separate future feature.

Staff directory/authentication/profile remain global control-plane resources.
Business operations require BOTH the existing role permission AND an active,
current-DB application grant. Proposed default: no implicit business-data wildcard,
even for SUPER_ADMIN; global SUPER_ADMIN manages applications/staff/grants and
can explicitly grant access under audit. This grant-management detail needs G5
acceptance before its endpoints activate. No new role hierarchy is introduced.
Unknown/ungranted apps are uniformly not-found; a granted app with insufficient
operation permission returns 403. DB lookup failures fail closed with generic 500.

## Data isolation invariants

Every application-owned aggregate and child/reference carries app_id: users,
segments/members, templates, installations, immutable bindings, campaigns/targets,
deliveries, receipt facts/read projections, idempotency records, outboxes and
source-event handling/hold evidence. Deferred upload resources must follow the
same rule if implemented. Global staff actions have no app; business audit events
must carry the resolved app. Secret-bearing snapshot rules remain in force.

Use explicit app-qualified queries and service checks, plus composite reference
constraints such as (app_id, user_id) -> users(app_id,id). Globally unique internal
row IDs may remain, but an ID alone cannot authorize a lookup, join or mutation.
Uniqueness for external subjects/segment names/idempotency includes the owning app
and applicable namespace/operation/principal. Evaluate RLS separately as defense
in depth; a column or FK alone is not authorization, nor is RLS claimed implemented.

One physical device may host apps A and B; they have distinct installation IDs,
secrets, versions, owner epochs and bindings. An installation never moves to a
different app. Account switching is within its established app; historical
bindings and delivery attribution remain immutable. Lost-proof recovery must
not steal another app's registration.

A Firebase address is opaque, not proof of app ownership. Within a provider
project/address namespace, prevent the same active address from being claimed
by conflicting app installations. Scope the address-conflict check to its
server-known project/type and protected address fingerprint, retaining immutable
historical rows; do not simply replace global token uniqueness with per-app
uniqueness and allow duplicate cross-app claims. M03 defines the exact partial
unique constraints/recovery protocol. Do not expose the conflicting app/owner.

## API, campaign and receipt scope

Proposed modern application-owned paths use `/api/v1/apps/{app_id}/...`.
The app selector is checked against authenticated authority; body app IDs cannot
override it. Returned resource IDs, pagination, counts, search, segment expansion,
statistics, exports and errors must all use the same scope. Global staff auth/
profile/control-plane routes remain separately protected.

A campaign belongs to exactly one immutable app. BROADCAST means all eligible
active devices for that app, never every app in the database. SEGMENT and
INDIVIDUAL targets cannot refer to another app. Scheduling, retries and replay
retain the original app and namespace. No global multi-app send endpoint is added.

Receipts require matching deployment/app, authenticated owner, original immutable
binding, owner epoch, campaign/delivery and signing capability context. Reusing
app A's capability/installation proof with app B's route must fail even if numeric
IDs/subjects coincide. Receipt dedup, stats denominators and read projections are
app-qualified; current permission alone cannot rewrite historical ownership.

## Provider routing, events and resource limits

Resolve Firebase project/app/platform and authorized credential version from
server-owned registry policy and the original binding/route snapshot. A client
cannot choose a project/credential/provider endpoint. Support multiple configured
FCM targets; project and credential principal are separate concepts and IAM must
authorize the target. Rotation may change a credential for the same authorized
project but may not redirect pending deliveries into another application's project.
Config/auth mismatch holds the affected route and alerts; it never tries another
app's credentials or silently falls back to a default SDK client.

Separate Firebase projects per logical production application are the recommended
isolation default, not an assumed inventory fact or an automatic provisioning
step. A shared project is an explicit exception requiring app-level origin/
identity isolation and shared quota/credential blast-radius review. Firebase
App Check is a candidate origin signal, not user authentication and not assumed
to be configured. If selected, verify the configured Firebase app-ID allowlist
as well as project/signature/expiry; project-only acceptance cannot separate
apps sharing that project. Token project compatibility alone is insufficient proof of an
app's user/installation ownership.

Persist app_id in business events/outboxes/DLQ/recovery context and check it
against the canonical resource before processing. Version the event envelope;
missing/mismatched app scope is held/rejected under a reviewed compatibility
path, never guessed from a token. Existing Kafka keys notification_<id> and
delivery_<id> remain while notification IDs are globally unique; changing key
bytes/partition mapping still requires ADR-07's migration review.

Use deployment/project-wide quota/concurrency budgets plus app fairness/limits;
per-app allowances cannot exceed a shared Firebase project's total budget.
Breaker and hold scope must reflect the failure's actual project/route boundary.
Application control rows belong to CR1's control/policy lock group, in stable
order before principals/campaigns/installation children. Define the concrete
transaction protocol in the owning SPEC; never acquire an app parent lock late.
Disabling an app holds new acceptance/admission/attempts for it, not unrelated
apps. In-flight provider sends cannot be recalled; this adds no CANCELLED feature.

## Legacy transition and release gate

1. Inventory/backfill existing data to one explicitly selected app using reviewed
   evidence. Unknown rows stay quarantined/held; no automatic first/default-app
   inference, fabricated ownership or unverified OneSignal-to-FCM conversion.
2. Add application-aware schema, grants, queries and modern routes with fixtures.
   Application rows may be provisioned disabled, without second-app business data.
3. Before introducing/activating another app's business data, retire every global
   legacy data route or replace it with a reviewed single-app compatibility bridge
   whose app is server-bound to approved callers and whose queries are scoped.
   This includes reads/search/stats, not only send/registration. Every legacy
   worker/scheduler/fan-out/replay job must also be scoped or drained/stopped:
   an old app A broadcast can otherwise include app B rows even if app B is held.
   New worker event fields alone do not fix an old global audience query.
   A dispatch hold alone does not prevent API data disclosure. No optional-app fallback.
4. Prove A/B isolation and per-app issuer/project/owner/recovery invariants, then
   enable each app independently with its evidence/cohort gate. Rollback must not
   re-expose global routes to a database containing multiple applications.

## Required acceptance evidence

Two apps must share coincident external IDs and installation labels in fixtures.
Test cross-app reads/writes/target joins, forged path/header/body scope, absent/
revoked grants, stale JWTs, enrollment/address conflicts, immutable history,
capability replay, same idempotency key, copied/missing event scope, project
misrouting, app hold/fairness, scoped stats and sealed restore. Include same-project
and different-project fixtures. Failed requests must leave DB/outbox/Kafka and
provider-call observations unchanged. Gate all legacy routes before app B data
appears. These tests have NOT run; existing single-app tests do not establish them.

## Sources and interpretation

Firebase documents multiple configured SDK apps and project configuration in
[Admin setup](https://firebase.google.com/docs/admin/setup), and sender/project
mismatch in [FCM error codes](https://firebase.google.com/docs/cloud-messaging/error-codes).
[Custom-backend App Check verification](https://firebase.google.com/docs/app-check/custom-resource-backend)
describes verified origin evidence. The isolation model, project recommendation,
DB/app grants and rollout rules above are this backend's proposed design; those
vendor documents do not establish that our deployment is isolated or configured.
