# Scoped delivery load experiment

This opt-in fixture measures a broadcast from its durable queue timestamp through
preparation, synthetic HTTP provider responses, committed device outcomes and
Kafka update drain. The worker executable, Firebase SDK, PostgreSQL and Kafka
are the production code paths used by the isolated delivery deployment.

## Reproduce

Run from the repository with its Go toolchain, Bash, Python 3 and a local Docker
Compose installation supporting `!override` and `!reset`. Container image
digests are pinned in `compose.integration.yml`. The runner accepts device and
campaign counts, provider behavior, campaign arrival pattern and an integer
per-second request limit. A sixth optional number sets the worker DB connection
allocation (2–32, default 2) for the one fixture replica. It accepts no endpoint
or credentials.

```bash
# Short CI smoke, including one real SDK call returning HTTP 503.
bash scripts/delivery-load.sh 32 2 transient burst 8 8

# Healthy single broadcast and a project-wide HTTP 429 exercise.
bash scripts/delivery-load.sh 20000 1 healthy burst 400
bash scripts/delivery-load.sh 20000 1 healthy burst 400 8
bash scripts/delivery-load.sh 64 2 quota soak 8 8

# Separate synthetic quota-headroom experiment.
bash scripts/delivery-load.sh 20000 1 healthy burst 1000 8

# Examples for simultaneous campaigns and repeated drained waves.
bash scripts/delivery-load.sh 2000 5 healthy burst 400
bash scripts/delivery-load.sh 1000 20 healthy soak 400
```

The project uses a single internal Docker network, zero published host ports,
disposable TLS/OAuth material and private DNS aliases for the two Google hosts.
Each invocation creates and removes its own containers and fixture data. The
worker verifies the PostgreSQL hostname, uses Kafka mTLS and authenticates with
the synthetic OAuth server. Its only permitted FCM route uses an encrypted,
synthetic Android registration token. The host needs enough memory for the
Docker VM and enough space for Go's build cache. Runs over 50,000 devices per
campaign, 25 campaigns, or 100,000 total deliveries are rejected. The load
driver caps the run, including setup inside the container, at 50 minutes.
Small CI runs use a shorter bound based on pace and wave count.
The ordinary Go test suite does not launch this experiment.

Fixture setup seeds one user and binding per device, then inserts a scoped
QUEUED campaign and calls the real set-wise preparer. The current fixture installs the ownership finalizer and application lifecycle guard,
then activates enrollment through separate issuer and executor logins. The operator
applies one grant per binding. Two further reviewed grants release the synthetic
pilot and then full dispatch before the timed campaign. HTTP send acceptance,
mobile enrollment calls, admission from the Kafka send topic and actual mobile
receipt arrival are outside the timed path. All burst campaigns are prepared
before worker startup. Soak mode waits for the preceding wave's completed
campaign and empty update outbox before preparing the next one.

## Measurements and assertions

Each provider call spends 15 ms in the synthetic handler after validation. The
provider counts physical requests, unique delivery IDs, responses, maximum
simultaneous requests and the highest number of arrivals in a sliding second.
The worker policy uses one replica, 16 concurrent submissions, a page of 100,
a 100 ms interval after each finished tick, a 10 s request timeout and three charged attempts.
The durable PostgreSQL pool defaults to two connections; the explicitly budgeted
comparison uses eight.

The runner checks that every device reached SENT with provider evidence and
version 1, every campaign completed, all charged attempts match physical HTTP
requests and every outbox event drained. It checks the broker's offset range
and, after stopping the timing clock, decodes that range to match distinct
device IDs and Kafka keys. A healthy case must have precisely one call and one
update per device. The provider-arrival check allows the configured per-second
allocation plus already-admitted concurrent work between pacing and the wire.

In transient mode the first submission for one registration in each campaign
receives 503. In quota mode the provider returns one 429 with `Retry-After: 60`
and then recovers. The test checks the observed physical retry interval. For
quota waves exceeding twice the concurrency, it must also observe PostgreSQL
deferring an uncharged, unclaimed pending row while the project hold is active.

The provider and database share the Docker VM's clock. The provider timestamps
completion of its response handler. PostgreSQL's attempt-resolution timestamp
comes from the transaction that persisted the result. The runner reads the
committed rows and reports p95/p99 per-device latency, as well as time from
the first queue record to the last result. It samples outbox depth and drain
every 500 ms. The drain stopwatch starts just before inserting the first queue
row and stops after observing committed completion and an empty outbox. The worker memory field is its kernel-reported peak resident set.

## Local baseline and selector diagnosis

The November 7, 2026 UTC runs used Linux ARM64 containers, an 8-vCPU Docker
28.1.1 VM with 8,218,316,800 bytes of RAM, Go 1.26.10, PostgreSQL 17
with temporary filesystem storage and a single Kafka 4.3.1 broker/partition.
The healthy case allocated 400 physical starts/s to one project and ran one
20,000-device campaign. These are individual observations on a shared
development machine. The historical baseline used the driver and schema from before the route grant
migrations. The current driver requires the managed application and binding
tables. Commit `173a34f` preserves the historical measurement harness;
`cef371c` contains the selector and lock changes.

With the original selector and 10,439 due updates, a rolled-back
`EXPLAIN (ANALYZE, BUFFERS, TIMING OFF)` of the scoped selection read all
20,000 intents, joined 10,439 events, and spilled a 9,448 kB external sort
before locking one row. It used 72,052 shared buffer hits and took 81.929 ms.
The fixed query uses the existing due-order and predecessor indexes to select
and lock the first eligible event before joining its immutable intent and
application identities. At 14,854 pending updates, it selected one candidate
with zero temp writes in 0.497 ms with a custom plan and 0.087 ms with a warm
generic plan. The generic-plan experiment changed the setting in one
rolled-back diagnostic transaction. The application uses normal PostgreSQL
planning. No schema migration is required for this fix.

The PostgreSQL integration regression uses 20,000 prepared intents and 10,000
due outbox rows. It checks the real sqlc selector's buffer accesses and temp
writes. Functional tests cover an unregistered scope, a foreign earlier record,
the unscoped publisher, a locked predecessor, recovery from uncertain Kafka
acknowledgement, and a foreign-lineage predecessor.

## Healthy results

Every run below ended with 20,000 distinct accepted registrations, 20,000
physical requests, 20,000 charged attempts, 20,000 SENT version-1 rows, one
COMPLETED campaign, 20,000 Kafka updates and an empty outbox. The updated
driver also decoded each published record and matched its individual delivery
ID. The matched runs use the control settings in the preceding section. Other
integration checks also ran on the host during part of the measurements. Times
are seconds from durable queueing. The last column records the
highest provider arrival count in any sliding second and concurrent requests.

| Selector / campaign lock / DB pool | Preparation | Last provider response | Last durable result | p95 / p99 result | Kafka drain | Sampled peak outbox | Peak requests/s / in flight |
| --- | ---: | ---: | ---: | --- | ---: | ---: | --- |
| Original / exclusive / 2 | 8.587 | 236.967–237.002 | 237.002 | 226.723 / 234.541 | 778.596 | 14,944 | 239 / 11 |
| Due-first / exclusive / 2 | 8.203 | 148.260 | 148.264 | 140.837 / 146.768 | 150.212 | 1,781 | 280 / 10 |
| Due-first / exclusive / 8 | 5.100 | 156.341 | 156.358 | 150.806 / 154.347 | 156.612 | 523 | 271 / 12 |
| Due-first / shared device reads / 2 | 4.917 | 135.978 | 135.980 | 131.097 / 134.785 | 140.423 | 2,339 | 280 / 10 |
| Due-first / shared device reads / 8 | 5.556 | 95.688 | 95.689 | 91.650 / 94.908 | 96.063 | 560 | 291 / 11 |

The original driver measured the interval between physical arrivals. For its
last provider response the table gives the enclosing interval from the final
durable claim to final committed result. Later runs timestamped the response
at the provider. The baseline driver completed its count and timing assertions.
Editing the outer shell during that run caused Bash to reread a changed tail
afterward. The repeated migration setup exited with an existing-table error and
cleanup removed the project. Subsequent measurements used a frozen script and
completed with exit code zero.

Sampling both worker connections 1,000 times approximately 20 ms apart in the
due-first exclusive trial found 811 observations waiting for the client in a
transaction and 187 waiting on an exclusive campaign row lock. With eight
connections, 3,364 of 4,800 observations waited on that campaign row. Increasing
the pool alone did not improve end time. Reserving, validating and recording
individual device work now share-lock the campaign. These operations still
lock and fence each specific attempt, and campaign changes still require
exclusive access. The corresponding eight-connection sample saw zero
PostgreSQL lock waits. The worker's peak resident set was 47,284 kB; one
container sample during this trial reported worker/driver 94%, PostgreSQL 120%,
Kafka 129%, and synthetic provider 7% CPU, where 100% represents one vCPU.
These observations do not isolate a single CPU limit on the shared VM.

For the 400-start/s setting, each page of 100 starts has at least 99 pacing
intervals. A single campaign uses 200 pages plus 199 completed-tick waits of
at least 100 ms. Even before preparation, the expected minimum is about 69.4 s
when starts and results are drained page by page. Quota, page, concurrency and
inter-tick policy must be evaluated together when setting a deployment target.

### Separate quota-headroom trial

The final worker ran a fresh 20,000-device broadcast with the synthetic quota
raised to 1,000 starts/s, the same eight DB connections, a page of 100 and
concurrency 16. Preparation took 6.063 s. The provider completed in 71.552 s
and the last result was recorded at 71.554 s (p95 68.610 s, p99 70.893 s).
The outbox peaked at 3,441 sampled updates and drained at 75.570 s. The provider
saw 20,000 calls, a peak of 400 arrivals in one second and 16 simultaneous
requests. The worker peaked at 49,696 kB resident. All 20,000 charges, SENT
rows, event IDs and Kafka keys matched. This run took 11.554 s longer than the
proposed healthy 60 s result target and averaged about 280 recorded devices/s
from queue acceptance.

## Backpressure and repeated campaigns

The short burst smoke queued two campaigns of 32 devices before starting the
worker, with one HTTP 503 injected per campaign. The shortest physical retry
interval was 10.063 s. Exactly 66 requests and 66 charges produced 64 SENT
rows and 64 distinct Kafka updates. Peak arrivals were eight/s at the configured
eight/s, with one request in flight. All rows and updates drained in 16.025 s.

In a two-wave run at eight starts/s (64 registrations per wave, eight worker DB
connections), the first provider request received HTTP 429. PostgreSQL retained
63 other pending, unclaimed devices with a future retry time and zero charged
attempts. Those rows stayed deferred during the project hold. The first device
retried 63.513 s after the rejection. Across both waves, the provider observed
129 calls, 128 acceptances, one rejection, a peak of eight arrivals/s and one
request in flight. PostgreSQL recorded 129 charges and 128 SENT outcomes; Kafka
contained exactly 128 distinct updates. Queue-to-last-result was 76.569 s and
the outbox drained in 77.009 s.

## Target-environment gate

The proposed healthy measurement is 20,000 durable device results within 60 s
from queue acceptance, approximately 334 results/s. Kafka drain is recorded
separately. Production SLO, approved headroom, sustained soak duration, provider quotas
and deployment sizing still require owner agreement. Repeat in staging on the
chosen storage and network with realistic application/epoch skew, payload
sizes, campaign mixes, process replicas and shared-project quota allocations.
Monitor DB waits, I/O, memory, broker lag and recovery during worker restarts,
dependency outages and provider throttling. Verify controlled real-device
delivery on Android/iOS as a separate cutover gate.

## Active polling comparison

Two additional healthy 20,000-device bursts ran in the same isolated synthetic
fixture with an eight-connection worker pool, concurrency 16, 100 devices per
page and a 100 ms idle interval. The optional seventh argument selected a 10 ms
active interval. The control rows are the earlier fixed-cadence runs above.
All times below are seconds from initial queue eligibility. The Kafka drain is
the first observed terminal campaign with an empty outbox, followed by broker
readback of every distinct delivery ID.

| Starts/s allocated | Active ms | Last provider reply | Last committed result | Result p95 / p99 | Kafka drain | Sampled peak outbox | Peak arrivals/s / in flight |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 400 | 100 | 95.688 | 95.689 | 91.650 / 94.908 | 96.063 | 560 | 291 / 11 |
| 400 | 10 | 74.833 | 74.835 | 70.990 / 74.239 | 78.054 | 7,689 | 348 / 10 |
| 1,000 | 100 | 71.552 | 71.554 | 68.610 / 70.893 | 75.570 | 3,441 | 400 / 16 |
| 1,000 | 10 | 48.843 | 48.845 | 45.923 / 48.332 | 56.981 | 9,250 | 617 / 16 |

Both additional runs produced exactly 20,000 charges, provider requests,
acceptances, SENT rows, version-one results and distinct broker delivery IDs.
Each ended with zero outstanding outbox rows or active requests. Preparation
took 6.038 s and 3.969 s respectively. Worker peak resident memory was 50,256 kB
and 47,428 kB. The faster submission increased the peak backlog waiting for
Kafka. The 1,000/s run finished submissions within the proposed 60-second
window locally. The 400/s run exceeded it by 14.833 s. These single trials use a
private simulated provider, local PostgreSQL and a single broker. Production
acceptance still requires repeated runs with representative storage, approved
project-wide capacity, concurrent campaigns, latency, failure and broker policy.

```sh
bash scripts/delivery-load.sh 20000 1 healthy burst 400 8 10
bash scripts/delivery-load.sh 20000 1 healthy burst 1000 8 10
```

## With mandatory per-binding cutover approval

The route-aware worker uses the same one-campaign, 20,000-device fixture, 1,000
starts/s allocation, eight database connections, 16 in-flight calls and 10 ms
active polling. Its fixture seeds an approved route revision for every synthetic
binding before starting the clock. Both measured runs froze each delivery's
audience revision and checked current authorization at reservation and before
the physical call.

| Latest route lookup | Preparation | Last committed result | Kafka drain | Peak sampled outbox |
| --- | ---: | ---: | ---: | ---: |
| Join to all latest binding revisions | 25.008 s | 104.946 s | 105.021 s | 203 |
| Indexed lookup by application and binding | 9.337 s | 64.363 s | 64.850 s | 3,840 |

An execution plan from the first fixture showed the join scanning the project's
route history inside the binding loop. The scoped lookup selects one latest
revision using the unique applied-grant index and both identity columns.
The indexed run observed 20,000 accepted physical requests, 20,000 unique
broker delivery IDs and zero outstanding outbox rows. Peak worker resident
memory was 47,524 kB. The 48.845 s result above predates per-binding authorization.
With the route checks enabled this trial exceeded the proposed 60 s threshold
by 4.363 s. These are single-host synthetic measurements. Actual authorized
provider capacity and target end-to-end timings are still required.

## Managed application and dispatch release

The managed fixture activates enrollment through the installed lifecycle guard,
approves all 20,000 binding routes with a separate operator, and then applies
distinct pilot and full dispatch grants. All grants and provider credentials
are synthetic. The clock starts when the campaign is durably queued, after
these setup operations. It includes preparation and worker startup. The run
uses the 1,000 starts/s allocation, 16 in-flight slots and 10 ms active
polling from the indexed comparison. The last trial changes only the worker
connection budget from eight to sixteen.

| Trial | Preparation | Last provider reply | Last durable result | Result p95 / p99 | Kafka drain | Peak sampled outbox |
| --- | ---: | ---: | ---: | --- | ---: | ---: |
| Intermediate managed build | 9.107 s | 59.628 s | 59.629 s | 57.677 / 59.256 s | 70.616 s | 3,501 |
| Final guard, 8 DB connections, concurrent verification | 10.202 s | 78.205 s | 78.210 s | 74.189 / 77.398 s | 84.215 s | 4,093 |
| Final guard, 8 DB connections, separate run | 9.069 s | 61.947 s | 61.948 s | 59.598 / 61.556 s | 65.076 s | 1,329 |
| Final guard, 16 DB connections, separate run | 9.023 s | 61.845 s | 61.847 s | 59.233 / 61.290 s | 66.543 s | 1,909 |

Each trial ended with exactly 20,000 distinct accepted provider calls, charges,
SENT rows and Kafka delivery IDs, and an empty outbox. Peak worker resident
memory was 47,524 KiB on the intermediate build, 49,236 KiB during concurrent
verification, 49,104 KiB on the separate eight-connection run and 47,892 KiB
on the sixteen-connection run. The final guard freezes the
pilot cohort on preparation and denies dispatch to older workers throughout
the pilot. A targeted integration drill shared the host during the 78.210 s run.
With other checks finished, the same eight-connection budget recorded all
results 1.948 s over the proposed target. Increasing the worker budget to
sixteen connections yielded 61.847 s, still 1.847 s over the proposal, and a
longer Kafka drain. Peak provider arrivals/s were 427, 466 and 456 across the
three final runs. These individual observations do not establish target
capacity or headroom. [Managed release procedure](../runbooks/application-dispatch.md).
