# M02b — Current-authority staff administration

Contract established 2026-11-03, using the confirmed SUPER_ADMIN-only policy.
Implementation/evidence status is recorded in `../analysis/m02b-verification.md`.

## Contract

All seven existing `/api/v1/staff` operations require `staff.manage`, granted
only to an active account whose current database role is exactly SUPER_ADMIN.
API key and staff JWT authentication remain required. The JWT role claim is
not authority. Each route checks before decoding input; each public service
method independently checks before target lookup, hashing, mutation or audit.
Unknown roles deny; missing/deleted/inactive identity is 401; insufficient role
is 403; lookup failure is a safe 500. Direct handlers map the same service errors.
Canonical permissions and staff routes live in `../contracts/staff-permissions.json`.

Password reset still requires the actor's current password, then the existing
new-password length validation, before target lookup or write. Actor/hash lookup
failures fail closed. Delete derives the actor from the verified principal and
continues to forbid self-deletion. Success statuses and DTOs remain unchanged.

Internal post-write response reads reuse the authorization already performed by
the public method, so an authorized self-demotion/deactivation can still return
its result and write the existing success audit. These helpers are private.
No cached route permission is trusted by the service.

## Verification required

- Four roles across seven routes, deliberately misleading token roles; authorized
  requests succeed and unauthorized requests leave staff/audit rows unchanged.
- Each public service rejects missing identity, lower/unknown roles, inactive or
  deleted actors and authority failures before touching business collaborators.
- Demotion between route and service checks denies the mutation; committed
  authority changes are read on the next request using the same JWT.
- Missing credentials fail closed; internal errors never disclose underlying detail.
- Reset success, wrong current password, length limits and actor lookup failures;
  self-delete denial and self-demotion/deactivation compatibility.

## Limits

Checks are statement snapshots, not locks through mutation. No session revocation,
last-super-admin protection, atomic audit transaction, password policy redesign,
body limits, resource-error cleanup or new role-management endpoints are included.
Existing best-effort audit/write-then-refetch failure windows remain. Real deployment
and external/mobile integration are separate gates.
