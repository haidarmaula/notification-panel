# M03 application-scope implementation plan

Date: 2026-11-09. Status: **M03a.1–3 registry and M03b.1–5 scope/caller/retirement slices are implemented and locally verified; production cutover and M03c/d integration remain open**.
Normative design: [ADR-12](../adr/adr-12-multi-application-isolation.md).
Each implementation slice needs its exact transaction/API/migration SPEC.
[M03a registry SPEC](m03a-application-registry.md) and its
[registry OpenAPI](../contracts/application-registry.openapi.json) are now written;
[M03a.1 schema/query/repository evidence](../analysis/m03a1-verification.md) is
now available; [M03a.2 services](../analysis/m03a2-verification.md) also pass local
checks. [M03a.3 HTTP](../analysis/m03a3-verification.md) is also local-verified.
[M03b.1 inventory/design](../analysis/m03b1-scope-inventory.md) now has an
[exact transition SPEC](m03b-application-data-isolation.md). [M03b.2a expansion](m03b2-business-scope-expansion.md) is now locally verified.
M03b.3 begins with the [scoped user/device query contract](m03b3-scoped-user-reads.md).
Staff HTTP reads now enforce application authority for users, devices,
[segments and members](../api/scoped-segments-api.md). Segment and membership writes enforce the same project boundary and commit
audit records in the mutation transaction. Final NOT NULL and scoped keys follow coordinated caller
replacement.

M03b.4 starts with [scoped campaign draft creation](../api/scoped-campaign-drafts-api.md).
Scoped list, detail, draft replacement and deletion share the same contract.
[Project template management](../api/scoped-templates-api.md) supports retirement
while retaining campaign references. [Campaign delivery and read history](../api/scoped-campaign-history-api.md)
now use project-scoped projections. [Upload and audit history](../api/scoped-upload-audit-api.md) exposes scoped
metadata. [Legacy retirement and the ownership finalizer](m03b-cutover.md)
prepare the deployment cutover.

## M03a — Registry and scope foundation

Define immutable application IDs and PROVISIONING-only registry status first.
The exact M03a SPEC defers provider/auth reference storage until the referenced
policy model is specified; there is no placeholder credential/config readiness.
Specify global SUPER_ADMIN control-plane operations and explicit
business app grants (G5 acceptance still needed). Add paired migrations/sqlc only
when implementing; do not hand-edit generated code. Establish a resolved
application principal/context and current-DB role+grant checker with no optional
or default app. No second-app business data or live enrollment in this slice.

Acceptance: app lookup/grant deny-by-default, grant revocation, authority DB failure,
no raw credential output/audit, immutable app identity, disabled app fixtures.

## M03b — Existing data/query/API scope and safe bridge

Inventory current tables, repository queries, background processes and every
legacy path. Map existing records explicitly or quarantine unresolved records.
Add app-qualified uniqueness/composite references, repositories and service checks
for users/segments/templates/campaigns/targets and their existing projections.
Global staff directory/profile/auth remains separate. Define modern
`/api/v1/apps/{app_id}/...` request/response contracts with presence/error/body
policy and versioned idempotency namespace; prohibit changing app ownership.

Treat this as focused per-aggregate sub-slices, with a final global-route inventory
gate. Existing unscoped queries must not see new app B rows. Retire legacy routes
or implement/test an explicitly server-bound single-app bridge BEFORE enabling
multi-app business data. Scope or drain/stop every old worker/scheduler/fan-out
process too; a held app B does not stop a global app A audience query from
selecting B devices. Activation may therefore wait for M05/M06; local fixtures
can test A/B in isolation without running legacy workers. Add A/B DB fixtures with duplicate external subjects;
prove cross-app IDs/joins/filter/count/DTO/audit/targets cannot escape scope.
Do not mark isolation done after adding a nullable app_id column.

## M03c — Immutable installation/binding and scoped auth

[Installation and binding contract](m03c-installation-binding.md) defines the
proposed storage, authority and lifecycle invariants.

Build the original M03 lifecycle contract on that scope: app-scoped external
identity, stable installation proof, immutable address/owner generations,
owner_epoch, refresh/logout/account switch and recovery conflict handling.
Factor mobile verification by trusted app issuer/audience/purpose policy;
no dependency on a particular React Native library or a universal Laravel claim.
Resolve actual provider/address-mode/attestation evidence per app/cohort.

Specify lock order/uniqueness/retries, protected registration-address storage,
app/project-wide address conflicts and tombstone/history retention floors before
schema generation. Legacy sync cannot create dispatch-eligible bindings through
an app-less/weak proof path. A lost installation secret never authorizes stealing
an address from another app/owner.

## M03d — Generic mobile contract and consumer validation

The [mobile lifecycle contract](../api/mobile-installations-api.md) is mounted
through the configured server policy. Local acceptance uses separate Android
and iOS HTTP clients against PostgreSQL. Their coincident subject stays scoped
by project and namespace. Cross-project JWT, origin and installation proof fail,
and shared-provider address collisions roll back enrollment. The passing suite
is recorded in /tmp/hello-m03d2-integration-final.txt (2026-11-04).

The kaiapps2 source checkpoint still shows OneSignal. Released-app integration,
broker/platform evidence and deployment policies remain open. [State reconciliation and provider invalidation](m03c-reconciliation.md) are
locally verified, including rollback and stale-generation tests. Provider result
wiring and deployment acceptance remain downstream gates.

Define enrollment, proof/version preconditions, same-owner refresh, logout,
account switch, pending receipt reporting and payload identity independent of
framework. The exact lifecycle path names and error/idempotency semantics belong
to this SPEC, not speculative endpoints advertised as available today.

Validate kaiapps2 as one first consumer after the platform contract exists.
A second synthetic client/app fixture must work without kaiapps2-specific claims,
business IDs or SDK assumptions. Laravel contract/config facts, Firebase project/
app/APNs and released mobile behavior remain per-app G3/G7 gates. No mobile repo
changes or live provider calls are authorized merely by this design document.

## Downstream propagation

M04: acceptance/idempotency/audit/outbox and scheduler preserve immutable app scope.
M05: scoped snapshot/admission and app-validated event/DLQ handling.
M06: configured app/project/platform provider clients and bounded shared budgets;
preserve existing Kafka key bytes until a reviewed mapping transition.
M07: capability audience/app/binding checks, scoped receipts/dedup/stats.
M08–M10: per-app observability/holds, environment isolation, restore and independent
cohort rollout. No cross-application broadcast or shared-user linking in v1.

Do not start a production multi-app claim until M03b's legacy-read/write gate
and end-to-end A/B negative tests pass; code scaffolding and existing M02 tests
are not that evidence.
