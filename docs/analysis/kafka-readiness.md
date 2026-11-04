# Kafka review and self-hosted readiness

Date: 2026-11-02. Status: design evidence, not provisioning/HA test results.
Design: [ADR-07](../adr/adr-07-kafka-topology.md).

## User confirmation

Kafka is planned for the company's own servers (self-hosted). The number of hosts,
failure domains/storage, resources and operators are not yet known. Three containers
on one server do not provide tolerance for losing one server.

## Actual source/dependency findings

| Finding | Evidence | Status |
| --- | --- | --- |
| H47: keyed partition affinity does not work | NewProducer uses LeastBytes; dependency balancer.go does not hash keys | Open; ADR-07 proposes Hash and a mapping rollout |
| H48: default batch wait risks dominating serial publishing | NewProducer does not set BatchSize/BatchTimeout; writer.go defaults to 100/1s; the processor waits for publish on every delivery | Open; low-latency config and benchmarks needed |
| M48: seed CSV is not an endpoint list | kafka.TCP(broker) and Reader.Brokers contain one string; dependency TCP is variadic and does not split CSV | Open; explicit validated slice needed |

Local probe (not a broker integration test), 2026-11-02:

```text
same key, partitions 0/1/2; LeastBytes: [0 1 2 0 1 2]
same key, partitions 0/1/2; Hash: [2 2 2 2 2 2]
```

The probe used Message.Key=notification_42, a fixed payload and six calls to
Balance in github.com/segmentio/kafka-go v0.4.51. Command executed:
GOCACHE=/tmp/hello-go-review-cache go run /tmp/hello-kafka-partition-probe.go.
The probe file was temporary; the SPEC must add a permanent regression test when
the producer is fixed. The probe made no network/provider/broker requests.

H48 is inferred from source/defaults, not broker measurements. If each serial publish
waits 1s for a flush, 20,000 publishes could add around 5.6 hours of batching
wait time in one loop; this illustrates a condition, not a benchmark result
or a definitive production estimate. Provider submission and analytics event latency
must be measured separately. M48 has also not been tested against a real multi-seed cluster.

## Production evidence checklist

| Gate | Required evidence | Status |
| --- | --- | --- |
| K01 host topology | Independent host/storage/network domains, broker/controller placement, resource budget | Self-hosted confirmed; footprint unknown |
| K02 versions/features | Pinned broker/client, KRaft quorum, feature/ELR and patch review | Compose version known; production unreviewed |
| K03 topics | Actual cluster/topic IDs, partitions/RF/ISR/retention/timestamp and internal-topic placement | Desired profile proposed; not provisioned |
| K04 clients | Hash golden vectors, batch/timeout bounds, parsed seeds, TLS/SASL and advertised connectivity | Current gaps registered; not fixed |
| K05 provisioning | Check/plan/apply artifact, drift detection, target-cluster guard, scoped admin | NEW artifacts not implemented |
| K06 group/retention | Explicit groups, offset range/expiry/reset behavior, outage/catch-up and dedup retention windows | Needs SPEC and consumer inventory |
| K07 security | Private listeners, ACL matrix, key/cert lifecycle and operator UI controls | ADR-11 pending |
| K08 failure tests | Real broker/host loss, low ISR, quorum loss, disk pressure, rebalance/offset loss and rotation | Not executed |
| K09 capacity | Hot-partition broadcast and event lag with resource/disk headroom | Not benchmarked |
| K10 operations | Alert actions, on-call, backup/restore coordination, rollout/rollback evidence | ADR-08/10 pending |

Proposed profiles: dev RF1/minISR1 (non-HA); production three partitions per
logical topic, RF3/minISR2, delete policy, 7-day log retention and 30-day inactive
offset retention. Final hosting, retention and SLO must be accepted before
production. Replication is not backup and retention is not a promise to retain
data forever until every consumer acknowledges it.

## Boundary of this increment

Step 12 is complete as a draft ADR and checklist. AGENTS.md was corrected so it no longer
presents LeastBytes as hash routing; code-review.md received new
open findings. Go code, compose, Makefile, topics/groups/offsets and credentials
were not changed. There was no live Kafka test, deployment, migration or FCM send.
