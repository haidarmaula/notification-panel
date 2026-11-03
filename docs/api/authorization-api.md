# Staff operation authorization contract

Status: **implemented in code, locally verified; not deployed**, updated 2026-11-03.
Notification/segment/member and staff administration APIs enforce the confirmed matrix. C21's scoped
operation bypass is remediated; see [evidence and remaining limits](../analysis/m02a-verification.md),
[SPEC](../specs/m02a-staff-operation-permissions.md), and
[canonical role/route matrix](../contracts/staff-permissions.json).

Multi-application requirement update: this implemented matrix checks operation
roles, not app membership. [ADR-12](../adr/adr-12-multi-application-isolation.md)
and [proposed API scope](application-scope-proposal.md) require a new application
boundary and A/B tests before multi-app activation; those guards are not shipped.

All routes retain API key (`X-API-Key`) plus staff access Bearer JWT.
Permission is checked against the active staff account/current DB role;
the token's role claim and UI visibility are not authority.
[M02e](../specs/m02e-staff-jwt-parser.md) additionally requires HS256, exp and a
positive staff ID for access/refresh parsing. Full session/issuer policy remains open.

| Role | Existing business reads | Draft/segment/member mutations | Send/schedule | Staff administration |
| --- | --- | --- | --- | --- |
| VIEWER | Allowed | Denied | Denied | Denied |
| OPERATOR | Allowed | Allowed | Denied | Denied |
| ADMIN | Allowed | Allowed | Allowed | Denied |
| SUPER_ADMIN | Allowed | Allowed | Allowed | Allowed |

[M02b](../specs/m02b-staff-administration.md) adds current-DB `staff.manage`
route/service guards to all seven staff endpoints; see [local evidence](../analysis/m02b-verification.md). Operational
hold/requeue/restore needs a separate scoped operator policy, not SUPER_ADMIN
role alone. The matrix does not add owner-only or tenant-specific restrictions.

Notification create/PATCH with a non-null `scheduled_at` needs schedule
permission as well as draft write permission. Null/absent retains legacy nil
semantics, not clearing/canceling a schedule. Current PATCH persists a timestamp
without scheduling a DRAFT; durable scheduling is a later contract change.
Existing non-DRAFT edit/delete/send restrictions remain in force.

Gate errors use the existing envelope:

| HTTP status | Meaning | Safe message |
| --- | --- | --- |
| 401 | Authentication/active account missing | `authentication required` (earlier API-key/JWT messages may differ) |
| 403 | Current identity lacks endpoint/field permission | `insufficient permissions` |
| 500 | Authority lookup/internal failure | `internal server error` |

```json
{"success": false, "message": "insufficient permissions"}
```

An operation denial has no business mutation, provider/Kafka publish or success
audit. Creators/audit actors come from verified staff identity; clients cannot
select another creator. Success statuses/DTOs stay as existing behavior. No new
error-code field, OpenAPI-wide rewrite, session rotation or transactional
revocation guarantee is shipped simply by writing this contract.
