# Application registry API

M03a exposes provisioning project metadata and explicit staff grants. All projects
remain `PROVISIONING`; these endpoints do not enable mobile enrollment or isolate
the legacy notification pipeline. See the [OpenAPI contract](../contracts/application-registry.openapi.json),
[exact SPEC](../specs/m03a-application-registry.md), and [local verification](../analysis/m03a3-verification.md).

Every operation requires `X-API-Key` and a staff `Authorization: Bearer ...` token.
Current active staff and DB role are checked at the route and service. Token role
claims confer no authority. Every registered operation returns `Cache-Control: no-store`,
including authentication/permission errors. Unknown paths/unsupported methods use
the existing server mux behavior.

| Method and path | Permission / result |
| --- | --- |
| GET `/api/v1/apps` | All four roles; list only explicitly granted metadata |
| GET `/api/v1/apps/{app_id}` | Explicit current active grant, including SUPER_ADMIN; detail + ETag |
| GET `/api/v1/admin/apps` | SUPER_ADMIN; global registry list; optional exact `key` |
| POST `/api/v1/admin/apps` | SUPER_ADMIN; `{ "key":"my-app", "name":"My app" }`; 201 + Location + ETag |
| GET `/api/v1/admin/apps/{app_id}` | SUPER_ADMIN; metadata + ETag |
| PATCH `/api/v1/admin/apps/{app_id}` | SUPER_ADMIN; `{ "name":"New name" }` + If-Match; 200 + ETag |
| GET `/api/v1/admin/apps/{app_id}/staff-grants` | SUPER_ADMIN; list active and revoked grants |
| GET `/api/v1/admin/apps/{app_id}/staff-grants/{staff_id}` | SUPER_ADMIN; grant + ETag |
| PUT `/api/v1/admin/apps/{app_id}/staff-grants/{staff_id}` | SUPER_ADMIN; `{ "is_active":true }`; conditional enable; 201 first row, otherwise 200; ETag |
| DELETE `/api/v1/admin/apps/{app_id}/staff-grants/{staff_id}` | SUPER_ADMIN; empty body + If-Match; soft revoke; 204 with no body |

`app_id` is the public UUID, never the internal database ID. `staff_id` is a
positive canonical decimal int64 string. Returned revision/staff IDs are strings
to preserve precision in JavaScript. Project DTOs contain only id, key, name,
status, revision and UTC timestamps; grants contain application_id, staff_id,
is_active, revision and UTC timestamps. Creating a project grants nobody access.
Staff roles remain global; project grants do not delegate global management.

Lists use the existing `{success,message,data}` envelope, where data contains
`items`, `total`, `page`, `limit`. Defaults are page=1/limit=20, bounds 1..1000 and
1..100. Empty pages use `items:[]` and retain the eligible total. Count and page
come from one SQL snapshot. Unknown, malformed or duplicate query parameters
return 400. No cross-request pagination snapshot is promised.

Writes accept exactly one UTF-8 JSON object, at most 65536 bytes including
whitespace, with exact case-sensitive field names. Required fields cannot be
null. Unknown fields, duplicate decoded keys (including escaped spelling),
multiple JSON values, invalid UTF-8, controls in names and invalid types return
400. Names are trimmed after rejecting controls and must occupy 1..128 UTF-8
bytes; keys are immutable lowercase ASCII identifiers per OpenAPI. New JSON
writes require `application/json`, optionally `charset=utf-8`; missing/other media
or parameters and non-identity Content-Encoding return 415. DELETE requires a
bounded empty body; even whitespace returns 400. These rules are opt-in and do
not change the existing legacy JSON endpoints.

For PATCH/DELETE, GET the resource and send its exact strong ETag in `If-Match`.
For PUT's first grant row, send `If-None-Match: *`; for an existing row, including
reactivation, send the current grant's `If-Match`. Missing conditions return 428;
stale conditions return 412. Weak tags, tag lists, duplicate headers, both
conditions, empty values and unsupported wildcards return 400. POST accepts no
condition headers. Matching no-ops keep the revision and emit no new audit;
revoke/regrant advances revisions and old tags stay stale.

Authentication/management authorization precedes path/body validation. Missing
or ungranted projects both return 404 on membership routes; malformed IDs return
400. Missing grants/staff return 404, duplicate project keys or inactive grant
targets return 409, oversized bodies return 413. Unknown errors return 500 with
literal `internal server error`, without database details or request values.
Mutations and allowlisted audit snapshots commit together. The service rechecks
authority inside its transaction; this is statement-time authority, not a promise
to cancel an already in-flight operation after revocation.

Before deployment apply migration 000016 and its audit-label preflight described
in [database documentation](../database.md). No activation or second-application
business data is permitted until M03b scopes or retires the legacy APIs, worker,
scheduler and fanout. Production deployment, provider/device delivery, performance
and infrastructure resilience remain separate release gates.

Scoped campaign send and scheduling use the separate
[durable acceptance contract](campaign-acceptance-api.md).
