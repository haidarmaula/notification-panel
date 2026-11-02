# SPEC — Isolated dependency integration environment

Status: step 19 test infrastructure; not an implementation of ADR-01–11.
Scope: real PostgreSQL migrations and Kafka round trips, plus real Firebase SDK
HTTP calls against a local FCM-shaped Postman stand-in. No application binaries,
real provider credentials, live device tokens, auth server or mobile app.

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
ADC/OAuth/APNs, provider quotas, retry timing and production adapter wiring are
outside the evidence. Unsupported provider operations return 501. Calls are
restricted to the local simulation by a test-only transport and no-auth SDK
configuration. Production provider code/config is not changed for a test bypass.
The bridge is not an outbound firewall; safety also depends on starting no
application workers and the fixture-only HTTP transport. Docker Desktop does
not publish host ports for the internal-network profile tested in this session.

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
| Ordinary go test ./... | Integration smoke is not run implicitly | None |

Failure-window/rebalance/lease/outbox/receipt integration coverage belongs to
later milestone SPECs. Passing this environment cannot close runtime findings
or prove the new notification pipeline, FCM/APNs or production throughput.
