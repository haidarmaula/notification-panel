# M02d — JSON request limit verification

Status: implemented and locally verified, 2026-11-03; not deployed.
[SPEC](../specs/m02d-json-request-limits.md),
[shared API contract](../api/request-bodies-api.md).

## Result

All 17 incoming JSON decoding handlers use `internal/httprequest.DecodeJSON` or
the auth media-type wrapper `DecodeJSONWithMediaType`.
The fixed budgets are 64KiB for small account/device/segment requests and 1MiB
for notification create/update, including bounded explicit target lists. These
are initial safeguards, not benchmark-approved final values. ADR-11's earlier
caps were illustrative; its implementation checkpoint now records these values.
No startup/config/schema/repository-interface changes were needed.

The helper first reads the whole bounded body via MaxBytesReader. Known large
Content-Length rejects early; missing/unknown or smaller length hints cannot
bypass the reader cap. It then unmarshals one complete JSON value, so a small
valid prefix plus excessive whitespace cannot cause a service mutation before
oversize detection. Within-budget multiple values/trailing garbage now return
400 instead of silently ignoring data after the first value. Unknown fields,
duplicate-key/null decoding and existing field validation remain compatible.

Safe existing-envelope errors are 413 `request body too large` or 400
`invalid request body`, without fragments/parser diagnostics. Existing route
auth/permission checks remain before decoding. Direct handlers also enforce
limits; their existing identity/path checks can run first. No global prebuffer
or changes to body-less operations are introduced.

## Checks actually run

All checks exited 0 using the repository's pinned Go 1.26.10 and modules:

| Check | What it establishes |
| --- | --- |
| `go test ./internal/httprequest/... ./internal/authz/...` | Reader boundaries/error safety, all 16 direct handlers, current authz baseline |
| `go test ./internal/httprequest/... ./internal/features/...` | Focused feature compilation/unit baseline (initial implementation pass) |
| `go test ./...` and `go build ./...` | Full default unit baseline and all binary compilation |
| `go vet ./...` and `go vet -tags=integration ./tests/integration` | Default/tagged static checks |
| `go test -race ./internal/httprequest/... ./internal/authz/...` | Focused local race checks |
| `bash scripts/ci.sh format` | Tracked Go formatting; new files also formatted with gofmt |
| `bash scripts/integration.sh` | Complete isolated dependency/security/audit suite plus request limits |

Reader tests include exact/over budgets, UTF-8 byte counts, known/unknown and
inaccurately small length hints, early-rejection zero reads, at most limit+1
bytes returned from the observed underlying reader, safe read failures, nil
body, invalid/empty JSON, extra documents, unknown fields and duplicate/null
compatibility. Body close behavior is checked. These reader counts do not claim
to measure net/http transport drain behavior or total concurrent memory use.

Direct-handler tests use nil services, so any accidental business call fails.
Each of 16 handlers rejects a large JSON value, valid-prefix oversized whitespace
and multiple values: 48 cases. Profile identity and required path IDs are supplied.

`TestJSONRequestLimitsAPI` composes all seven real modules with the current
server middleware arrangement, repository queries and a rollback-only PostgreSQL
fixture. Synthetic staff/mobile JWTs exercise the existing authenticators.
Across all 16 decoding routes, known-large, unknown-large and multiple-value
bodies return 413/413/400 (48 route cases). Full ordered business/audit row
snapshots remain unchanged on each rejection. Both Kafka topic end offsets
remain unchanged across these tests and successful draft creates.

Additional cases prove missing API key/JWT and current-DB role denial precede
body errors. Exact-budget segment (64KiB) and campaign (1MiB) requests return
201 through the actual modules. A real localhost HTTP/1 server/client exchange
observes `Transfer-Encoding: chunked` and Content-Length -1 and returns safe 413
without database changes. Other route cases use in-process HTTP recorders.

The final integration execution passed in 13.749 seconds, excluding environment
startup; the existing provider SDK simulation remains fixture-only. Owned Docker
resources and the simulation process were cleaned up on successful runner exit.
Logs: `/tmp/hello-m02d-{focused,decode-tests,unit,build,vet,integration-vet,race,format}.txt`
and `/tmp/hello-m02d-integration.txt` (temporary local evidence).

## Limits and next step

M44's body-limit finding is locally remediated, including profile. At the time of
the original M02d implementation, M43 remained partial because Content-Type
enforcement was unchanged. No decompression, duplicate-key rejection,
protected-field policy, item/string/depth limits, timeouts/header limits or
aggregate resource limits ship in that slice. The helper does not bound
hashing/provider payloads or decoded work independently of the byte budget. No
load/abuse or reverse-proxy test ran.

M02 and plan step 20 remain in progress. Reassess the baseline and specify the
remaining security controls before moving to M03; do not infer complete
production readiness. Hosted CI, deployed behavior and external/mobile caller
compatibility remain unverified. No new dependency scan ran (no dependency
changes); earlier scans are historical evidence. Production audit cleanup from
M02c remains a separate operator gate.

Follow-up 2026-11-03: [M02 assessment](m02-baseline-assessment.md) and
[M02e](m02e-verification.md) complete the scoped local development guard review.
M03 isolated design/implementation may proceed; security release controls remain open.

Follow-up 2026-11-09: M43 first added media-type checks to login, refresh and
logout. It now applies the same policy to the mounted staff and profile JSON
handlers; mounted scoped application handlers already used the strict JSON
decoder. Direct tests cover missing and wrong media types across these handlers,
while the shared helper/auth tests cover duplicate and compressed headers.
Unsupported media returns 415 before service work. Caller compatibility and
deployment remain unverified. The full `go test ./...` suite passed after
updating the authorization test fixture to send its declared JSON media type.
`bash scripts/integration.sh` also passed: `hello/tests/integration` in 148.416s,
`hello/internal/fcm` in 1.651s, with 75 SDK requests observed by the simulator.
The route inventory/API route checks, Go build/vet/format checks and OpenAPI
JSON syntax check passed. These checks do not verify real caller compatibility.

The auth route contract and JSON body-limit tests passed in the full isolated
integration run on 2026-11-09. The direct-handler limit fixture now includes the
previously omitted logout endpoint, bringing its route count to 17. The earlier
16-handler figures above describe the original M02d run.
