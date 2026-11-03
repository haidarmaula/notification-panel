# Multi-application design checkpoint

2026-11-03. Design work only; no source/schema/runtime changes in this checkpoint.

The user confirmed a reusable backend for multiple mobile applications.
kaiapps2/React Native is one initial consumer, not a platform dependency.
[ADR-12](../adr/adr-12-multi-application-isolation.md) proposes the boundary;
[M03a–d](../specs/m03a-application-scope-plan.md) sequences implementation.
[API namespace proposal](../api/application-scope-proposal.md) is not an available API.

## Current code evidence

| Source inspected | Present behavior / implication |
| --- | --- |
| db/migrations/000001_create_users.up.sql | external_id is globally unique; no application namespace |
| db/migrations/000002_create_device_tokens.up.sql | user FK and push-token uniqueness are global; no app context; existing installation_id is not immutable app-bound proof |
| db/migrations/000008_create_notifications.up.sql | Campaign has no application FK; current audience is not app-qualified |
| internal/authz/authorization.go | Current role operation checker, no app membership grant; M02 verification cannot prove application isolation |
| internal/kafka/producer.go | Existing globally unique notification-ID key formats must be preserved pending an explicit mapping migration |
| ADR-04/05/06/11 | Already require project/app/owner evidence in places, but do not define a complete business application registry/data/access boundary |

No application Firebase project, key, APNs config or mobile source was inspected.
Only prior read-only branch inventory exists for the user-authorized kaiapps2
repository. No changes were made to that repository.

## Design changes recorded

- Application is an immutable business data/identity/permission boundary.
- Android+iOS can be clients of one application; other frameworks share the contract.
- Per-app trusted auth/origin and provider route policy; no global Laravel assumption.
- App-qualified users, campaign/audience/binding/receipt/event/recovery context.
- Existing role permission plus explicit app grant; grant-management details proposed.
- Independent staging/production deployment/resource/secret boundary.
- Registry-owned Firebase configuration and quota/fairness boundaries; shared
  Firebase project is an explicit reviewed exception, not assumed isolation.
- Legacy global query gate BEFORE any second-app business-data activation,
  including scoped or drained/stopped worker/scheduler/fan-out processes.
- Cross-app negative cases required alongside current single-app reliability tests.

The original optional ADR-12 product-parity placeholder in the production plan
was not a published ADR. Number 12 now identifies the concrete application-scope
ADR; optional product parity stays deferred/unassigned. Existing ADR-01–11 file
paths and the 46 production step numbers remain unchanged. CR10 records the
cross-ADR amendment; CR1–CR9 invariants are preserved.

## Validation and limits

Reviewed the listed current schema/authorization/key-format sources and affected
architecture documents. Checked local Markdown links, diff whitespace, roadmap
references and consistency of the design's stated boundaries. No Go tests were
rerun because no Go/SQL runtime files changed; passed M02 evidence remains scoped
historical evidence. No new A/B isolation, migration, provider or production test
was run. The candidate schema/API/grant details remain proposed.

Verified vendor references on 2026-11-03: Firebase Admin setup supports separately
configured apps; FCM documents sender mismatch; App Check custom-backend docs
explain project verification and optional app-ID filtering. This design requires
checking the allowed app identity if App Check is selected for app separation;
project-only validation is insufficient for that purpose. Vendor capabilities
do not prove our backend's authorization or registration-address ownership.

Next: write the exact M03a registry/scope schema and API SPEC, then implement it
with fixtures. M03b's all-legacy-route/read/write gate precedes activating a
second application's data. M03c/d define generic installation/mobile contracts;
consumer inspection validates those contracts rather than dictating their design.
App grant policy, per-app issuer/origin evidence, legacy mapping and provider
inventory remain G3/G5/G7 acceptance items. Number of apps, aggregate device load
and concurrent campaigns remain G2 sizing inputs; the earlier 20k/5-campaign
baseline must not silently become a per-app or fleet capacity guarantee.

## M03a contract checkpoint

The [exact registry SPEC](../specs/m03a-application-registry.md) and
[OpenAPI 3.1 contract](../contracts/application-registry.openapi.json) now define
10 proposed routes, global-management versus caller-granted metadata, UUID/immutable
key, PROVISIONING-only creation, soft grant revocation, conditional ETags and
atomic mutation/audit. Provider/auth binding storage is deferred until its exact
policy models exist; metadata cannot claim operational readiness.

Validation at this checkpoint parses JSON, resolves local OpenAPI references,
checks ten unique operations/security/permission/body/response declarations and
example/schema invariants, plus local documentation links and diff whitespace.
This is structural/custom validation, not a full OpenAPI validator or a live API
run. No Go/schema/runtime changes, new DB tests, migrations or endpoint execution.
Next concrete work is M03a.1 paired migration, queries, sqlc and DB constraints;
then resolver/transaction services and HTTP/regression cases. All earlier multi-app
activation, legacy query/background process and G3/G5/G7 gates remain open.
