# M03 application-scope implementation plan

Date: 2026-11-03. Status: **M03a.1–3 registry locally implemented/verified; M03b.1 inventory/design complete; M03b.2a expansion locally verified; business isolation and M03c/d pending**.
Normative design: [ADR-12](../adr/adr-12-multi-application-isolation.md).
Each implementation slice needs its exact transaction/API/migration SPEC.
[M03a registry SPEC](m03a-application-registry.md) and its
[registry OpenAPI](../contracts/application-registry.openapi.json) are now written;
[M03a.1 schema/query/repository evidence](../analysis/m03a1-verification.md) is
now available; [M03a.2 services](../analysis/m03a2-verification.md) also pass local
checks. [M03a.3 HTTP](../analysis/m03a3-verification.md) is also local-verified.
[M03b.1 inventory/design](../analysis/m03b1-scope-inventory.md) now has an
[exact transition SPEC](m03b-application-data-isolation.md). [M03b.2a expansion](m03b2-business-scope-expansion.md) is now locally verified.
Next: M03b.3 scoped users/devices/segments; final NOT NULL/scoped keys must
follow coordinated caller replacement. Business isolation remains pending.

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
