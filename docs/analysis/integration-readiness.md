# Dependency integration evidence

Date: 2026-11-02. Step 19/M01 dependency foundation, tested locally on
darwin/arm64 + Docker Desktop. [SPEC](../specs/integration-environment.md),
[runner](../../scripts/integration.sh), [Compose](../../compose.integration.yml),
[tests](../../tests/integration/dependencies_test.go).

## Rerunning

Tested prerequisites: Go 1.26.10, Docker 28.1.1 with a running daemon, Compose
2.35.1, golang-migrate CLI v4.19.1, Postman CLI 1.75.0 and Python 3.
PostgreSQL/Kafka images are pinned by registry digest; pulling requires network access if
the images are not already available. Local runs require no Postman login/workspace/key.

```bash
bash scripts/integration.sh
```

In this session's sandbox, Go used GOCACHE=/tmp/hello-go-review-cache and
GOMODCACHE=/tmp/hello-ci-modcache. Docker socket/localhost required runtime
permission outside the sandbox. This does not change application credential requirements.

The runner uses project `hello-integration`, loopback ports 15432/19092/14010,
public fixture DB/user/password values, temporary PostgreSQL/Kafka storage, and
temporary simulation logs. The development `.env` is loaded by neither Compose
(`--env-file /dev/null`) nor application config loaders. No application server,
worker or scheduler is run. The development compose file and
Makefile are unchanged. A separate bridge is not an outbound firewall; the Firebase test
transport restricts every request to simulation loopback and rejects redirects.

A lock prevents parallel runs. If the project/ports are already in use, the runner refuses;
it does not take over or remove existing resources. Cleanup covers only
the project newly created by the runner and its own simulation PID, without Docker prune
or stopping Docker Desktop. After a hard crash, inspect the reported project/process
and lock before manually cleaning up fixtures.

## Observed results

| Check | Result / evidence limit |
| --- | --- |
| Actual images | PostgreSQL 17.10 and Kafka 4.3.1; digests are in Compose; RF1/1 partition, not HA |
| Migrations | All 15 migrations passed up → down-all → up on a fresh fixture DB |
| Actual repository | database.New + generated sqlc + UserRepository Create/FindByExternalID and four seed roles passed |
| Actual producer | Both topics were explicitly provisioned; NewProducer published an event and the reader received the same key and JSON |
| Actual Firebase SDK | Send against accepted, UNREGISTERED, quota 429, unavailable 503 fixtures produced the expected response/error classifications |
| Deadline | The caller stopped at about 100 ms rather than waiting for the 2-second delayed response |
| Egress test | A real provider request URL was rejected by the local transport before network I/O; no credentials/real devices were used |
| Simulation log | 9 actual SDK POST requests; statuses 200×2, 404×1, 429×1, 503×5; including a delayed response after caller timeout |
| Guard | Busy ports were rejected; an existing project container was preserved after refusal; unknown arguments exited 2; the tag without explicit opt-in failed rather than skipping |
| Cleanup | Success and test-failure paths removed fixture containers/network; the pre-existing-container case did not remove that container |
| Checks | Ordinary go test ./... still passed, tagged go vet passed; Bash/JS syntax and Compose config were validated |

The SDK generated five physical HTTP requests for one logical Send on 503
because of internal retries. This is fixture-scenario evidence, not a real-provider timing
promise. The M06 executor/rate/attempt design must account for it; this step does not
change SDK retry policy or the production adapter.

The complete smoke suite passed with exit 0. Raw runtime logs were placed in a
temporary directory and deleted, not committed; the runner checks request
events so success establishes more than a mock starting successfully. Simulation
context retains provenance and uncertainty for subsequent runs.

## Integration limits and follow-up

Owner mock lookup was unavailable because the Postman CLI was signed out; this does not claim
that no provider mock exists. The local stand-in was built from Firebase REST send/error
documentation and the pinned Go SDK, then tightened for fixture requests.
CLI 1.75 requires a manifest file `path`, rather than a directory. The mock/simulation
was not pushed, deployed or published to a workspace.

FCMProvider currently uses SendAll; the worker still uses OneSignal. The harness calls
Firebase SDK Send directly, **not** the application adapter/worker. Not yet demonstrated:
admission/outbox/leases, rebalance/offset safety, multi-partition affinity,
provider selection/credentials, token enrollment/receipts, real FCM/APNs/devices,
20k throughput, multi-host failure or restore. Linux/GitHub-hosted execution
and an automatic integration job were not run by this step.

M01 is complete as checks + a repeatable local environment. Next is
step 20: SPEC slice M02 permission/API baseline; G5 needs to be settled for the policy
being implemented. Add integration/failure cases at every milestone,
rather than treating dependency smoke tests as proof of reliability for the entire backend.

M03a.1 update 2026-11-03: the runner now exercises 16 migrations, including
registry/grant/audit-scope constraints and real-DB CAS/locking/round-trip tests.
See [evidence](m03a1-verification.md). The table above retains the original
15-migration checkpoint; no new live-provider or production evidence is implied.
