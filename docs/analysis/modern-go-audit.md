# Modern Go audit

Date: 2026-11-04. Scope: `internal/dispatch`, `internal/installation`, their
database contracts, mobile callers and existing tests. Standard:
[Go engineering standards](../go-standards.md). This review does not certify the
whole repository or close production integration/release gates.

## Findings

| ID | Severity | Trigger | Fix |
| --- | --- | --- | --- |
| GA1 | P2 | A caller without a deadline reaches installation pool acquisition/project locking, mobile identity queries or replay consumption; slow/blocked DB work has no internal end. Lifecycle rollback can also outlive cancellation indefinitely. | Five-second operation contexts reach identity/origin stores and all lifecycle transaction callbacks. Lifecycle retries share the original deadline. Cleanup is detached from cancellation and bounded to one second. |
| GA2 | P2 | An active Kafka generation calls source approval without a request deadline and PostgreSQL blocks. | Each database approval check has a five-second context, including normal Fetch calls. Idle Kafka waits retain their original cancellation semantics. |

These bounds do not change authority, proof verification, immutable binding
attribution, lock order, transaction isolation or idempotency. Unknown errors
keep the existing safe HTTP response. Commit uncertainty still requires state
reconciliation; timeouts do not authorize automatic replay of external effects.

## Verification

- Red tests reproduced absent deadlines in authentication/origin/source approval
  and all four lifecycle entry points.
- A temporary mutation doubling the lifecycle limit was rejected by the regression
  tests; the original five-second limit was restored byte-for-byte.
- Focused unit/race tests verify bounded pool/query calls, a shared retry budget,
  shorter caller deadlines and independent cleanup after cancellation.
- The PostgreSQL regression holds the application row on another connection and
  checks deadline expiry, zero partial state, pool release and successful reuse.
  It passed in approximately 5.14 seconds. The full opt-in integration suite
  passed with `GOFLAGS=-race`, including Kafka generation handoff and the existing
  local Firebase SDK HTTP simulation. No real provider/device was used.
- Full unit/race, build and vet checks pass. The pinned skill snapshot also
  passes its offline integrity check. Hosted CI and production are not verified.
- The reachable vulnerability gate reports zero affected vulnerabilities. It also
  reports one imported-package and three module findings whose vulnerable paths
  are not reached by this build; those are not claimed to be remediated.

No other concrete P1/P2 defect was confirmed in the reviewed modern paths.
Additional coverage opportunities include concurrent quarantine sealing/source
deactivation, explicit preparation retry exhaustion and partial recovery reports.
Those are test gaps, not evidence that the current contracts are broken.

## Remaining work

The modern worker/scheduler libraries remain unwired. Operator reprocessing,
FCM execution/leases, receipts, operational instrumentation and production gates
remain tracked in the [production plan](production-readiness-plan.md). Historical
legacy findings, including H47/H48/M48, are not closed by this audit.
