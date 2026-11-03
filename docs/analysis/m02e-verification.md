# M02e — Staff JWT parser verification

Implemented and locally verified 2026-11-03, not deployed.
[SPEC](../specs/m02e-staff-jwt-parser.md),
[M02 baseline assessment and next sequence](m02-baseline-assessment.md).

## Findings and change

The exit review inspected the two staff parsers and pinned jwt/v5 v5.3.1.
The library treats exp as optional unless explicitly required. A temporary
fixture-only probe confirmed that both parsers accepted HS384, HS512, missing
exp, zero ID and negative ID tokens correctly signed with synthetic keys. Normal
tokens were accepted too. No real secrets/server were used; the temporary Go
probe source was removed. Diagnostic log:
`/tmp/hello-m02-evaluation-jwt-probe.txt` (before the fix).

This was a parser policy gap, not evidence of unsigned/unknown-key forgery or an
operation permission bypass: signature checks and the existing DB guards remained.
The two parsers now pin HS256, require exp and reject nonpositive user_id in
addition to the existing signature/purpose checks. All failures return nil
claims and ErrInvalidToken without library diagnostics. Generators, TTLs,
HTTP DTOs, role handling and Laravel/mobile parsers are unchanged.

## Verification actually run

All final checks below exited 0 with Go 1.26.10 and the pinned module set:

| Check | Evidence |
| --- | --- |
| `go test ./internal/token/... ./internal/middleware/... ./internal/authz/...` | Parser policy, pre-lookup HTTP failures and existing authentication/permission checks |
| `go test ./...`, `go build ./...`, `go vet ./...` | Full default baseline, all binaries and static checks |
| `go vet -tags=integration ./tests/integration` | Tagged integration static checks |
| `go test -race ./internal/token/... ./internal/middleware/... ./internal/authz/...` | Focused local race checks |
| `bash scripts/ci.sh format` | Tracked formatting; new tests also formatted with gofmt |
| `bash scripts/integration.sh` | Complete dependency/API/audit/body-limit suite plus staff JWT tests |

`TestStaffParserPolicy` covers 16 invalid variants for each purpose (32 cases):
HS384/HS512/none, wrong secret, missing/null/invalid/expired exp,
missing/zero/negative/fractional/string identity, future nbf and missing/wrong
purpose. Every rejection has nil claims. Existing generated-token round trips
pass; new tests cover equal-secret purpose isolation and informational role claims.

`TestStaffHTTPRejectsBeforeLookup` covers three malformed-policy variants for
access/refresh (six cases). The access middleware's staff probe is never called;
refresh runs the actual auth route/service with nil DB queries, proving rejection
before a query could run. Responses are safe 401 and never echo the token.

`TestStaffJWTPolicyAPI` verifies the same six failures with real PostgreSQL/module
wiring. Generated access and refresh still succeed. A stale VIEWER refresh claim
for staff 700001 with DB role ADMIN produces ADMIN in the new access token;
this reconciles old C2 against the existing correct staff.RoleID lookup.
M02a/b inactive/current-role tests remain green; C8's original no-active-check
claim also no longer matches current middleware, while session revocation remains open.

The final integration run passed in 13.731 seconds, excluding startup. Initial
attempt could not connect because Docker was stopped; Docker Desktop was started
and the rerun passed. The runner cleaned its owned fixture resources/simulator.
Provider checks remained local HTTP simulation, not real FCM/APNs.
Logs: `/tmp/hello-m02e-{focused,token-tests,unit,build,vet,integration-vet,race,format}.txt`
and `/tmp/hello-m02e-integration.txt`.

No fresh vulnerability scan ran (no dependency changes). No live deployment,
hosted CI, external token migration or React Native/Laravel execution was verified.
The full issuer/audience/key/session/mobile/abuse policy remains open per the
assessment; M02e does not claim to complete ADR-11 or production security.
