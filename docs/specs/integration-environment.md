# SPEC — Isolated dependency integration environment

Status: step 19 test infrastructure; not an implementation of ADR-01–11.
Scope: real PostgreSQL migrations and Kafka round trips, plus real Firebase SDK
HTTP calls against a local FCM-shaped Postman stand-in. The delivery-worker
case also starts the production Linux binary using synthetic credentials and
an isolated HTTPS provider.

The runner uses a dedicated Compose project/network, disposable
storage and fixed loopback-only host ports. It explicitly disables Compose
`.env` loading and does not merge the development compose file. It provisions
both existing topics with auto-create disabled; one broker/RF1 tests are not HA.
Normal unit tests remain independent; integration tests require an explicit tag.

NEW artifacts: `compose.integration.yml`, `scripts/integration.sh`,
`tests/integration/dependencies_test.go`, and local Postman mock/contract/simulation
under `postman/`. Fixed fixture credentials are public, local test-only values.
The script starts only its own resources, uses bounded readiness waits, and
removes only its dedicated Compose project and owned child processes on exit.
Unknown commands/missing prerequisites/unavailable dependencies fail nonzero;
no automatic test skip. It must not stop unrelated containers or Docker Desktop.

The HTTP fixture models documented send response/error envelopes and SDK
serialization. Fixture address names select responses, not real FCM behavior.
The Postman error scenarios use a loopback test transport. A separate binary
test runs the production worker and its HTTPS fixture on an internal-only Docker
network. The runner checks both containers' network attachment. PostgreSQL and
Kafka expose authenticated TLS listeners on that network. The worker trusts
the disposable CA and reads root-owned policy and service-account files. The
HTTPS fixture verifies the synthetic account's OAuth assertion and accepts the
scoped FCM request. See the [worker runbook](../runbooks/delivery-worker.md).

## Acceptance cases

| Input / condition | Expected result | Expected error |
| --- | --- | --- |
| Clean run | Isolated dependencies become healthy; all tagged smoke cases execute; owned resources cleaned | None |
| Existing development .env or unrelated containers | Not loaded/started/stopped by this runner | None |
| Docker/Postman absent, daemon unavailable, port busy | Bounded early failure; owned partial setup cleaned | Nonzero exit |
| Existing migrations | All up migrations succeed; seed roles present; down/up replay on test DB succeeds | None |
| Existing database repository | Create/read synthetic active user on migrated DB | None |
| Existing Kafka producer, both provisioned topics | JSON/key read from broker matches actual published event | None |
| Fixture accepted address via Firebase SDK Send | Synthetic message resource name returned; request present in simulation log | None |
| Fixture UNREGISTERED | SDK identifies registration rejection | Typed SDK error |
| Fixture 429 / 503 | SDK identifies quota/unavailable; contexts bound any SDK retry | Typed SDK error |
| Fixture response slower than request deadline | Caller stops by deadline | Context timeout |
| Nonlocal SDK URL / redirect | Test transport rejects request before external I/O | Local transport error |
| Delivery worker with an expired lease and one eligible device | Publishes FAILED and SENT, makes one synthetic FCM send, finalizes the campaign, and exits cleanly on SIGTERM | None |
| Ordinary go test ./... | Integration smoke is not run implicitly | None |

Failure-window/rebalance/lease/outbox/receipt integration coverage belongs to
later milestone SPECs. Passing this environment cannot close runtime findings
or prove the new notification pipeline, FCM/APNs or production throughput.
