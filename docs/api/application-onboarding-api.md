# Application onboarding setup API

Status: **implemented locally; optional and not deployed by this artifact**.
The [OpenAPI contract](../contracts/application-onboarding.openapi.json) lists
only the routes mounted by the current setup runtime.

The protected setup API supports readiness, enrollment configuration, candidate
staging, OAuth verification, activation requests, test installation discovery,
cohort dry-run requests, and dispatch hold/release. Lifecycle and dispatch
execution use protected operator commands. Credential promotion remains pending.

The [frontend handoff](application-onboarding-frontend-handoff.md) provides the
step order, examples, polling rules and action gates for the separate UI session.
The [runtime contract](../design/application-onboarding-runtime.md) documents the
operator-only preparation and one-shot preflight commands. They add no HTTP
endpoint or lifecycle/release authority. A successful preflight is not a readiness
acknowledgement or evidence of an FCM send.

## Availability and authorization

Onboarding is an optional protected server capability. The default server
configuration leaves it unmounted. Mounting it requires the protected
`onboarding` server policy, an mTLS connection to the credential provisioner,
approved enrollment profiles, the database grants from
`deploy/postgres/server-grants.sql`. Verification also requires a
per-application verifier scope. If the routes are mounted without a verifier
scope, readiness reports `VERIFIER_NOT_CONFIGURED` and verification returns `503` with
`Retry-After: 5`.

The local runner uses the explicit `--local-onboarding` server command. This
retains ordinary local server settings but additionally requires the fixed,
root-owned `/etc/hello/onboarding.json` policy through the protected file reader.
There is no caller-supplied policy path or environment toggle. The production
`--deployment` command still uses its protected server policy without falling
back to local configuration.

The dashboard BFF forwards setup and release requests under
`/bff/api/admin/apps/{app_id}/onboarding`. It preserves setup ETags, idempotency
keys/replay acknowledgement, and bounded retry delays. Operation locations are
rewritten into the same app's BFF namespace. Queries and duplicate concurrency
headers are rejected, except that the test installation list accepts the
`after` cursor. Only FCM upload receives the 96 KiB outer-body budget;
other requests retain the 64 KiB limit. Browser writes still require the current
session, exact Origin and CSRF token. The dashboard setup screen is available at
`/apps/admin/applications/{app_id}/onboarding`, linked from registry detail. It
supports profile configuration, file upload and OAuth polling, with explicit
review after a stale version and same-request retry after an unknown outcome.
Credentials remain in component memory only and are cleared after definitive
upload success or navigation; they are not stored in browser storage or query
caches. The dashboard also supports enrollment activation requests, test cohort
selection and dry-run review, and PILOT and FULL release requests. The operation
and readiness responses determine which actions are available and distinguish
queued requests from completed changes.

Every route uses the existing API key and staff bearer authentication chain and
the current database-backed `applications.manage` guard (SUPER_ADMIN). A token
role claim alone does not grant access. Application and operation IDs are
canonical UUIDs; resources are scoped to the requested application. Onboarding
does not create staff grants or make an application member-visible. Requests
with query parameters are rejected. Responses carry `Cache-Control: no-store`.

## Mounted routes

| Method and path | Request | Success |
| --- | --- | --- |
| `GET /api/v1/admin/apps/{app_id}/onboarding` | — | `200` readiness; strong setup `ETag` |
| `GET /api/v1/admin/apps/{app_id}/onboarding/enrollment-profiles` | — | `200` configured profile list |
| `PUT /api/v1/admin/apps/{app_id}/onboarding/enrollment` | Profile ID and platform identifier mappings | `200` readiness; updated `ETag` |
| `PUT /api/v1/admin/apps/{app_id}/onboarding/fcm` | Environment, target project ID, service-account JSON | `201` candidate metadata; updated `ETag` |
| `POST /api/v1/admin/apps/{app_id}/onboarding/fcm/verify` | Configuration ID; optional test installation ID | `202` queued verification operation and `Location` |
| `GET /api/v1/admin/apps/{app_id}/onboarding/operations/{operation_id}` | — | `200` sanitized, app-scoped operation |
| `GET /api/v1/admin/apps/{app_id}/onboarding/approvals` | — | `200` safe approval metadata list |
| `POST /api/v1/admin/apps/{app_id}/onboarding/dispatch/hold` | Reason | `200` readiness; dispatch held |
| `POST /api/v1/admin/apps/{app_id}/onboarding/dispatch/release` | `mode`, `cohort_id`, `evidence_id` | `202` queued release operation and `Location` |
| `POST /api/v1/admin/apps/{app_id}/onboarding/activate` | `configuration_id` | `202` queued activation and `Location` |
| `GET /api/v1/admin/apps/{app_id}/onboarding/test-installations` | Optional `after` UUID cursor | `200` scoped installation page |
| `PUT /api/v1/admin/apps/{app_id}/onboarding/test-cohort` | `installation_ids`, `reason`, `evidence_id` | `202` queued cohort verification and `Location` |
| `GET /api/v1/admin/apps/{app_id}/onboarding/operations/{operation_id}/targets` | — | `200` per-target cohort status |

The response envelope is `{ "success": true, "message": "…", "data": … }`.
Errors use the shared envelope with `success: false` and `data: null`.
Unknown server errors return the literal message `internal server error`.
Operation IDs from another application return `404`.

## Revisions and replay

Readiness returns a strong setup ETag in this form:

```text
"onboarding:{app_id}:{revision}"
```

Every write requires exactly one `If-Match` header and one canonical UUID
`Idempotency-Key`. A new write must use the current ETag. A missing `If-Match`
returns `428`; a stale ETag on a new write returns `412`; malformed or duplicate
headers return `400`. Matching retries are checked after current authorization
and return the original result even after the setup revision advances.
All write replay windows are 24 hours. A retained key outside that window returns
409; it is not silently reused for new work. Submission retries return the original
acceptance snapshot. Poll the operation resource separately for its current state.

Mutation responses include `Idempotency-Replayed: true|false`. Enrollment, FCM
staging, and hold responses return the new setup ETag. A queued verification
returns an operation `Location`; its ETag is included when the operation result
has a revision.

## Request rules

Requests use `application/json`, UTF-8, and identity content encoding. Decoders
accept one JSON object with exact lowercase field names; unknown, duplicate,
malformed, or trailing fields are rejected. `400` indicates invalid input,
`413` an oversized body, and `415` an unsupported media type. GET routes do not
accept query parameters.

FCM candidate uploads are limited to 96 KiB total, with a 64 KiB maximum for
the nested service-account object. Other write bodies are limited to 64 KiB.
The service-account object is validated against the requested target project.
The private key is never returned. The server sends the candidate over mTLS to
the protected provisioner, which stores it in the protected credential store;
the API server does not write the protected credential file itself. A `201`
response means candidate metadata was stored. It does not make that candidate
current, activate the application, or establish IAM permission or device
delivery.

`PUT .../enrollment` accepts a configured profile ID and one or two unique
platform entries (`ANDROID` or `IOS`) with application identifiers. The server
checks each identifier against the selected profile's server-side allowlist.
This records setup only; it does not activate the application.

Verification accepts a configuration UUID and optional `test_installation_id`
UUID. Explicit JSON `null` is invalid. With the field omitted, the queued worker
checks the staged credential through OAuth. This is OAuth credential
verification only; it does not prove target-project IAM permission or device
delivery. Supplying a valid installation belonging to the same application
currently returns `503` with `Retry-After: 5` because target-permission dry-run
is not implemented. A foreign installation returns `404`. No raw device token
is accepted. At most five new verification jobs per application per minute are
accepted; the limit returns `429` with `Retry-After: 60`.

`GET .../onboarding` reports readiness checks, blockers, next actions, setup
revision, and current/candidate configuration IDs. A stale or expired OAuth
check may be reported as inconclusive. Readiness is a snapshot, not continuing
proof of provider access. `checks_configuration_id` identifies the candidate
when present, otherwise the current configuration; it is null without either.
Evidence for a replacement candidate does not attest the currently running
configuration. Missing `LOCAL_FORMAT`, `OAUTH`, `TARGET_PERMISSION` and
`DEVICE_DELIVERY` checks are explicitly `NOT_CHECKED` with null timestamps.
Only independently recorded evidence can report a check as passed; OAuth
success cannot fill target-permission or device-delivery checks.
`GET .../approvals` only lists existing safe metadata;
it does not issue approvals. `POST .../dispatch/hold` synchronously closes the
dispatch policy and leaves application lifecycle status unchanged.

## Activation and test cohort

The operator workflow starts with `issue-first-activation` and
`apply-first-activation`. These commands use separate database roles. An
activation request stays pending until the required runtime leases and the
credential verification are current. Once applied, the app is ACTIVE and dispatch
is HELD.

After enrollment, use the installation UUIDs from `GET .../test-installations`.
An operator uses `approve-cohort` to authorize the exact roster. Submit the
approval UUID as `evidence_id` on the cohort request, then poll the operation
and its `/targets` list. A target may show a null outcome before the verifier
finishes. PASSED establishes dry-run permission for that binding. The operator
then approves and applies the pilot route for the verified binding.

## Dispatch release

Submit `mode` (`PILOT` or `FULL`), the current `cohort_id`, and `evidence_id`
(the UUID of a matching, unconsumed onboarding approval, not a dispatch grant).
PILOT queues from HELD; FULL queues from PILOT. The application must be ACTIVE
with a current configuration and runtime generation. A `202` only records a
request; it does not change dispatch mode. Poll `Location` for completion.

A reviewed issuer creates the approval with `approve-release` and uses
`delegate-release` to issue and bind a dispatch grant to the accepted operation.
A separate operator runs `controlplane apply-release`; PostgreSQL revalidates
staff session authority, approval, runtime and the PILOT/FULL gates. FULL still
requires signed `DELIVERED` evidence, not merely provider acceptance.

A pending release with a current approval and a usable grant, or one still
awaiting its grant, occupies the mutation slot (`409` for another release).
If its approval becomes unusable or its unapplied grant expires or is revoked,
submit a valid same-mode request with the latest setup ETag and a new idempotency
key. The API atomically marks the old operation `SUPERSEDED` with failure code
`RELEASE_GRANT_UNAVAILABLE` and queues the new one. A replay of the old key still
returns its original acceptance. The old grant cannot be rebound; the issuer
must issue and bind a new one. A still-valid approval may be reused until a
release commits.

## Local verification

The fixture uses synthetic metadata and a network-isolated pinned Postgres
container. It does not contact Google or use real credentials:

```sh
go test ./internal/features/applications/...
go test ./internal/onboarding/...
scripts/application-onboarding.sh
scripts/server-grants.sh
scripts/application-onboarding-preflight-fixture.sh --preflight-services
```

The application-onboarding fixture exercises migration backfill, configuration
scoping, verifier roles and sessions, operation idempotency, and rollback
guards. The composed services fixture queues both release modes over HTTPS,
exercises grant expiry and revocation recovery, and runs the protected release
command. Its FCM provider and device receipt are simulated.
