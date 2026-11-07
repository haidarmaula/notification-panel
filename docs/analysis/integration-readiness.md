# Dependency integration evidence

Original checkpoint, 2026-11-02. Step 19/M01 tested the dependency foundation on
darwin/arm64 and Docker Desktop. [SPEC](../specs/integration-environment.md),
[runner](../../scripts/integration.sh), [Compose](../../compose.integration.yml),
[tests](../../tests/integration/dependencies_test.go).

The current suite includes the managed dispatch path, repair/replay, scoped
receipts, and subject-erasure impact checks. For the full sealed restore suite, run
`bash scripts/restore-drill.sh`. The [readiness plan](production-readiness-plan.md)
and [worker benchmark](delivery-load.md) track subsequent evidence.

## November 8 release-candidate verification

The clean backend source at `2d813fa` passed migrations 1–39 up, down and up
and the full integration suite under the Go race detector. The PostgreSQL and
Kafka package completed in 154.431 s and the FCM package in 3.288 s. The
simulator recorded 76 FCM SDK HTTP requests and three OAuth requests. The suite
included managed pilot scope, mixed-cohort rejection, erasure inventory and
the sealed backup restore using an independently confined probe.

The ordinary race suite, build, vet, module verification, SQL regeneration
and contract checks also passed. All six images passed offline entrypoint and
configuration checks as UID 65532. The vulnerability scan reported zero reachable
advisories and flagged one imported-package advisory and three module advisories
outside the analyzed call paths. This verification ran locally.

A separate encrypted PostgreSQL 17.10 recovery run on schema 39 rejected both
an incorrect key and missing WAL. It recovered to the selected position,
excluded the deliberately later campaign, and verified stored outbox state,
session logout and the new lineage while services remained sealed.
The selected target was 1.117 s behind source shutdown. Restoration through
sealed verification took 3.259 s in this small fixture. Deployed RPO and RTO
remain open. [Physical recovery drill](../runbooks/physical-pitr.md).

## Migration 40 and queued recovery verification

All 40 migrations passed up, down and up. The race-enabled PostgreSQL and
Kafka suite took 159.587 s and the FCM suite took 7.235 s. The simulator
recorded 76 Firebase SDK HTTP requests. The 20,000-device preparation
regression verified bounded row visits against the generated SQL. The queued
recovery regression verified that a failed campaign remained retryable while
the next campaign prepared. Logical sealed restore passed, and the independent
encrypted recovery drill on schema 40 again rejected a wrong key and missing
WAL. [Capacity evidence](delivery-load.md).

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
(`--env-file /dev/null`) nor application config loaders. The original dependency smoke ran without an application process. The current suite also exercises application processes with private fixtures. The development compose file and
Makefile are unchanged. A separate bridge is not an outbound firewall; the Firebase test
transport restricts every request to simulation loopback and rejects redirects.

A lock prevents parallel runs. If the project/ports are already in use, the runner refuses;
it does not take over or remove existing resources. Cleanup covers only
the project newly created by the runner and its own simulation PID, without Docker prune
or stopping Docker Desktop. After a hard crash, inspect the reported project/process
and lock before manually cleaning up fixtures.

## Original November 2 results

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

At the M01 checkpoint, FCMProvider used SendAll and the worker used OneSignal. That harness called
Firebase SDK Send directly, **not** the application adapter/worker. The M01 smoke did not cover
admission/outbox/leases, rebalance/offset safety, multi-partition affinity,
provider selection/credentials, token enrollment/receipts, real FCM/APNs/devices,
20k throughput, multi-host failure or restore. Linux/GitHub-hosted execution
and an automatic integration job were not run by this step.

M01 established a repeatable local environment. Step 20 then added the M02 permission and API checks.

M03a.1 update 2026-11-03: the runner now exercises 16 migrations, including
registry/grant/audit-scope constraints and real-DB CAS/locking/round-trip tests.
See [evidence](m03a1-verification.md). The table above retains the original
15-migration checkpoint; no new live-provider or production evidence is implied.
