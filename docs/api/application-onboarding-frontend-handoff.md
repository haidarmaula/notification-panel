# Application Registry onboarding frontend handoff

Date: 2026-11-18. Status: **setup and OAuth are available when explicitly
configured; end-to-end onboarding remains incomplete**. This handoff describes
the running contract, not the proposed lifecycle API. Use the
[implemented API](application-onboarding-api.md) and
[implemented OpenAPI](../contracts/application-onboarding.openapi.json) for wire
details. Backend runtime-preparation work does not add a dashboard capability.

## Active routes and sequence

All paths below are relative to
`/api/v1/admin/apps/{app_id}/onboarding`. The dashboard uses the same suffixes at
`/bff/api/admin/apps/{app_id}/onboarding`. The BFF preserves concurrency/replay
headers and rewrites operation locations into the same application's BFF path.
These routes are absent when the protected onboarding server policy is absent.
They require the current DB-backed `applications.manage` authority, API key and
staff bearer authentication. Browser access additionally requires the current
session and the BFF's Origin/CSRF protections. No token role claim substitutes
for current database authority.

| Step | Method and suffix | Successful result | UI action |
| --- | --- | --- | --- |
| 1 | `GET` (empty suffix) | `200` readiness and setup ETag | Load the exact registered application and retain the ETag |
| 2 | `GET /enrollment-profiles` | `200` profile list | Offer only configured profiles/platforms |
| 3 | `PUT /enrollment` | `200` readiness and new ETag | Save approved profile and Android package name/iOS bundle ID |
| 4 | `PUT /fcm` | `201` safe candidate metadata and new ETag | Upload a service-account object; clear the secret after definitive success |
| 5 | `POST /fcm/verify` | `202` VERIFY acceptance and operation Location | Verify OAuth using the candidate configuration ID |
| 6 | `GET /operations/{operation_id}` | `200` current operation | Poll accepted verification to a terminal state |
| 7 | `GET /approvals` | `200` safe approval list | Show existing approval metadata; no approval creation action |
| Incident | `POST /dispatch/hold` | `200` readiness and new ETag | Hold dispatch with a reason; this does not change application lifecycle status |

Enrollment configuration and FCM upload can be completed in either order. Fetch
GET readiness again after each write and each completed asynchronous operation.
Synchronous mutation responses and replay snapshots are not normalized evidence
views: their `checks` may be empty, and historical replay snapshots may have a
null `checks_configuration_id` until the replay window expires. A fresh OAuth pass
still leaves a new application `PROVISIONING` with dispatch `HELD`.

## Safe request and response examples

Examples use illustrative UUIDs and profile names. The selected profile and
application identifiers must match the protected server allowlist. The response
envelope is `{ "success": true, "message": "…", "data": … }`; examples below
show the `data` object unless stated otherwise.

```http
PUT /bff/api/admin/apps/11111111-1111-4111-8111-111111111111/onboarding/enrollment
Content-Type: application/json
If-Match: "onboarding:11111111-1111-4111-8111-111111111111:1"
Idempotency-Key: 22222222-2222-4222-8222-222222222222

{
  "profile_id": "33333333-3333-4333-8333-333333333333",
  "platforms": [
    {"platform": "ANDROID", "application_identifier": "com.example.pilot"}
  ]
}
```

FCM upload has this JSON shape; `<validated service-account object>` is a
description, not a literal accepted value. Do not embed a real credential in
documentation, logs, error reports, screenshots or frontend fixtures.

```text
PUT .../fcm
{
  "environment": "staging",
  "target_project_id": "example-pilot-project",
  "service_account": <validated service-account object>
}
```

A successful upload returns safe metadata such as:

```json
{
  "id": "44444444-4444-4444-8444-444444444444",
  "state": "STORED",
  "target_project_id": "example-pilot-project",
  "service_account_email": "sender@example-pilot-project.iam.gserviceaccount.com",
  "revision": "3"
}
```

Use the upload response's ETag for the next new write:

```http
POST /bff/api/admin/apps/11111111-1111-4111-8111-111111111111/onboarding/fcm/verify
Content-Type: application/json
If-Match: "onboarding:11111111-1111-4111-8111-111111111111:3"
Idempotency-Key: 55555555-5555-4555-8555-555555555555

{"configuration_id":"44444444-4444-4444-8444-444444444444"}
```

The `202` response contains an operation, with a same-app `Location` header:

```json
{
  "id": "66666666-6666-4666-8666-666666666666",
  "kind": "VERIFY",
  "state": "PENDING",
  "created_at": "2026-11-18T03:00:00Z",
  "completed_at": null,
  "failure_code": null,
  "resulting_revision": null
}
```

`POST .../dispatch/hold` accepts `{"reason":"Review provider configuration"}`
with the same write headers. No direct FCM-send endpoint exists. Supplying
`test_installation_id` to verification currently returns `503` for a valid
same-app installation because target-permission dry-run is not implemented;
a foreign installation returns `404`. Omit this field for OAuth verification.

## Revisions, replay and polling

Every write requires exactly one strong setup `If-Match` and one canonical UUID
`Idempotency-Key`. Metadata ETags from registry endpoints are different resources
and cannot be used here. Save the exact payload, ETag and key for an in-flight
write; disable conflicting local edits until its outcome is known.

After a lost response or uncertain transport failure, offer retry of that exact
request with its original key and ETag. The server checks matching replay after
current authorization and before rejecting an advanced ETag. A different payload
under the same key returns `409`. Replays return the original submission snapshot,
not the operation's latest state, and include `Idempotency-Replayed: true`.
Replay windows last 24 hours; expired retained keys return `409`. Do not generate
a new key automatically after an uncertain outcome.

For `412`, pause new writes, fetch readiness, show the changed state and require
explicit review before submitting a new request. Do not silently replace the
ETag and resubmit. For a successful synchronous write, retain the returned ETag;
after verification finishes, fetch readiness to obtain the current setup ETag.

Poll only the same-app operation Location returned by the API. `PENDING` and
`RUNNING` are nonterminal; `SUCCEEDED`, `FAILED` and `SUPERSEDED` stop polling.
The server does not prescribe a polling interval. A frontend policy may start at
two seconds and back off to ten seconds, pause while the view is hidden, and
cancel on navigation. Honor bounded `Retry-After` on errors; do not turn polling
errors into another verification submission. Preserve the operation ID so a view
reload can resume its observation. A terminal failure can have a safe
`failure_code`; never expect or display a raw Google response.

## States, blockers and action enablement

Display lifecycle and dispatch as separate facts:

| Field | Values | Meaning for the current setup screen |
| --- | --- | --- |
| `application_status` | `PROVISIONING`, `ACTIVE`, `HELD`, `RETIRED` | Enrollment lifecycle; setup alone cannot activate a new app |
| `dispatch_mode` | `HELD`, `PILOT`, `FULL` | Delivery policy; OAuth success does not open dispatch |
| Candidate `state` | `STORED`, `VERIFYING`, `VERIFIED`, `REJECTED`, `SUPERSEDED` | Credential-version metadata, not device delivery evidence |
| Check `outcome` | `NOT_CHECKED`, `PASSED`, `FAILED`, `INCONCLUSIVE` | Evidence result; stale/foreign-epoch OAuth passes become inconclusive |
| Approval `status` | `VALID`, `EXPIRED`, `REVOKED` | Safe metadata only; approval does not itself execute release |

GET readiness `checks_configuration_id` identifies the configuration described by
`checks`: candidate when present, otherwise current, or `null` before any
configuration exists. Always label evidence with that version; candidate evidence
must not be presented as proof for the running current configuration. The four
check codes are `LOCAL_FORMAT`, `OAUTH`, `TARGET_PERMISSION` and `DEVICE_DELIVERY`.
Missing evidence is explicitly `NOT_CHECKED`. OAuth success must not make target
permission or device delivery appear passed.

Show `checks`, `blockers` and `next_actions` from readiness. Known blockers are
`FCM_NOT_CONFIGURED`, `ENROLLMENT_NOT_CONFIGURED`, `FCM_NOT_VERIFIED`,
`VERIFIER_NOT_CONFIGURED`, `DISPATCH_APPROVAL_REQUIRED`, `RUNTIME_PENDING` and
`LEGACY_ADOPTION_REQUIRED`. Unknown codes should remain visible with a neutral
fallback instead of being treated as success. `PREPARE_RUNTIME` and
`CONFIGURE_VERIFIER` describe operator/backend prerequisites; they are not active
dashboard commands.

Enable profile save only after a valid profile/platform selection and identifiers
are present. Enable upload only with the chosen environment, target project and
valid file input. Enable OAuth verification only with a candidate/current
configuration ID, known current ETag and no unresolved write; unavailable verifier
scope is an actionable blocker. Backend validation remains authoritative.
Hold is available for an authorized incident action with a reason. Never enable
Activate, test-cohort approval, device-test, Promote, PILOT release or FULL release
from readiness text or because a handler/type exists in the source: these routes
are not mounted.

## Errors and input limits

| HTTP status | UI handling |
| --- | --- |
| `400` | Correct malformed input, UUIDs, strict field names, duplicate headers or forbidden query parameters |
| `401` / `403` | Reauthenticate or report missing current authority; stop retries |
| `404` | Resource absent or outside this application; do not probe another application |
| `409` | Review invalid state or idempotency conflict; fetch current state |
| `412` | Explicit stale-version review before a new write |
| `413` / `415` | Correct body size or content type/encoding |
| `428` | Supply the current setup ETag |
| `429` | Honor `Retry-After: 60`; at most five new verification jobs per app/minute |
| `503` | Show unavailable prerequisite; honor `Retry-After: 5` where returned |
| `500` | Display the generic `internal server error`; retain an uncertain write's retry identity |

Requests require UTF-8 `application/json` with identity content encoding, one JSON
object, exact lowercase keys, no duplicate/unknown fields and no trailing data.
Explicit `null` for the optional verification installation is invalid. Uploads
are limited to 96 KiB total and 64 KiB for the nested service-account object;
other writes are limited to 64 KiB. All responses carry `Cache-Control: no-store`.
Keep credentials in component memory only, clear after definitive upload success
or navigation, and exclude them from caches and browser storage.

## Remaining backend and real-environment gates

The intended sequence remains registration → setup → OAuth → enrollment
activation (`ACTIVE`/`HELD`) → external client enrollment and route/cohort review →
approved PILOT campaign and per-platform device receipt → explicit FULL release.
Only registration, setup, OAuth and one-way hold currently have running dashboard
contracts. Operators can reserve and prepare a stopped allocation, then run an
owner-approved one-shot delivery preflight under the same reservation. Neither
operation has an HTTP endpoint. Preflight starts only the startup-check process;
its result is not a readiness acknowledgement and does not change enrollment or
dispatch. Activation requires app-scoped live delivery authority, published shared
policies, current-generation acknowledgements and DB-delegated lifecycle execution. Device testing
requires approved identity mappings, scoped cohort/evidence handling and target
IAM checks. Release requires current campaign/provider/device evidence and guarded
delegation. Credential rotation requires held promotion/drain/recovery.

Local synthetic OAuth and existing-app simulator delivery establish different
facts: neither proves a new registry app can activate or receive a notification.
Target-project IAM, actual Firebase submission, Android/iOS device receipts,
production capacity and backup/restore need separate evidence in authorized real
environments. See the [implementation checkpoint](../analysis/application-onboarding-implementation.md)
for backend progress and validation limits.
