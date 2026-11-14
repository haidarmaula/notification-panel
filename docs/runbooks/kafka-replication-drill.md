# Authenticated Kafka replication and offset recovery

For host installation and the optional drill tools, see [machine setup](../setup.md)
and [operational prerequisites](../tooling/development-tools.md#operational-drill-prerequisites).

Run this drill with Docker, Go, OpenSSL and Python 3 from the repository root.
The pinned Apache Kafka 4.3.1 image must already be available locally. Each run
creates a unique Compose project, an internal network with no published ports,
three KRaft controller processes, three data brokers and an operator probe.
The runner generates a one-day synthetic CA and service certificates. Broker
and controller listeners require mutual TLS. The CA private key stays outside
the containers. The six server processes share one disposable node certificate.
The operator probe holds the synthetic admin and client credentials so it can
exercise each principal independently. Cleanup removes the project, volumes and
temporary keys.

```bash
GOCACHE=/tmp/hello-go-review-cache bash scripts/kafka-drill.sh
```

## What the drill verifies

Run the command with `--monitor` to add disposable Prometheus, Alertmanager
and a synthetic webhook receiver to the private network. Have the
digest-pinned images in [the monitoring overlay](../../compose.kafka-monitor.yml)
available locally. Fetching the pinned JMX Exporter release asset requires curl.
Set `HELLO_KAFKA_JMX_JAR` to the path of the release jar for an offline run
with the images already available.
The script checks the SHA-256 before starting Kafka and mounts the jar and
MBean allowlist read-only. Prometheus scrapes on the internal network with
zero published ports. Target deployments use an authenticated management path.
[Monitoring runbook](monitoring.md#kafka-replication).

- When all brokers stop, the real offline-partition rule must produce a
  firing notification at the fixture webhook through Alertmanager. After
  broker recovery, the webhook must accept a resolved notification with the
  same fingerprint. The receiver validates the fixture scope, alert name,
  severity and runbook reference before returning success.
- The checked-in fixture profile pins an environment, cluster ID, three explicit
  replica placements per partition, three partitions per topic, minimum ISR 2,
  delete retention, append-time timestamps, size limits, unclean election policy
  and seven literal ACL grants. The probe's default action plans changes. The
  runner explicitly applies missing entries and reads back broker metadata,
  effective topic settings and ACLs. An existing deployment with unexpected ACL
  or configuration drift fails verification. The drill temporarily changes a
  topic's retention by one millisecond, verifies that apply refuses it, and
  restores the intended value through its fixture operator.
- Both topic IDs from the Kafka CLI, controller quorum status and feature levels
  are captured before failover. Internal consumer-offset partitions must reach
  RF3 and three in-sync replicas before the drill starts. Each client uses its
  own certificate. Scheduler and operator replay can write send requests,
  admission can read the send topic and use its own group, the delivery worker
  can write updates and analytics can read them and use its own group. Probes
  attempt unauthorized writes, reads, cross-group commits and topic creation.
  A trusted certificate without an ACL cannot use either application's routes.
  API and bootstrap receive no Kafka identities in this profile.
- For each deliberately new, inactive fixture group the operator records
  numeric starting offsets for all three partitions. After writing and reading
  the first canaries, each reader commits its next position. The harness stops
  the elected controller and the verified leader for partition 2. It waits for
  a new data leader with ISR 2 and verifies the controller election. Clients
  recover their committed offsets, reread the first canaries and append and
  consume another record with all-replica acknowledgement. The production
  scoped writers also publish the same keyed envelopes on partition 0 before
  and after leader loss, including the seed address that became unavailable.
- The harness restarts the stopped controller and stops a second data broker.
  At ISR 1, both topics must return the Kafka
  `NOT_ENOUGH_REPLICAS` error for a `RequireAll` produce. After the two brokers
  restart, the drill requires full ISR, unchanged topic IDs and stable group
  checkpoints for every partition. The next acknowledged write must occupy
  the first unused offset and the recovered canaries must match their data.
- Admission's PostgreSQL-backed missing, stale, future and live-retention offset
  regressions run in the main integration fixture. Group bootstrap and
  reconciliation require a confirmed group/inactive-source boundary.
  [Admission offset procedure](../specs/m05-kafka-adapter.md#provision-and-reconcile-group-offsets)

## Disposable run recorded 2026-11-07 UTC

The full wrapper exited 0 and removed its containers and internal network.
Apache Kafka 4.3.1 reported metadata version `4.3-IV0`. Send topic ID
`RnIPTAV4Txq-rPw5q0wFIg` and update topic ID `7hLsEsa9RPayAI5931hjqQ`
matched after recovery. Controller 102 was replaced by 101, and broker 1
replaced broker 3 as leader for partition 2 of both topics. Stopping both
leaders through verified canaries and quorum took 19 seconds. At ISR 1, both
writes were explicitly refused. Restarting the two brokers through verified
full ISR, replayed records, group checkpoints and topic IDs took 11 seconds.
Each group finished at next offsets `[2,0,3]` for partitions 0–2. These elapsed
times describe one synthetic run on a single host.

## Monitored broker failure and recovery

In the November 8 isolated run, Prometheus scraped all six nodes and observed
one active controller. The replication alert fired with two in-sync replicas
while the minimum ISR gauge stayed at zero. At one in-sync replica, both
topics refused all-replica writes and the below-minimum ISR alert fired.
Stopping the last broker fired the active controller's offline-partition alert
and the broker scrape alert. Restart restored full ISR, acknowledged writes,
numeric group checkpoints and the original topic IDs. All six scrapes
returned and the Kafka alerts cleared. Kafka traffic used synthetic
credentials and an internal Docker network.

In the November 9 isolated repeat, the checkpoint sampler distinguished missing
commits, a one-offset lag and zero lag for both scoped fixture groups. The
groups' credentials could not inspect an unrelated group. Both readers resumed
with their committed offsets after the leader election and full broker restore.

A separate November 9 run added Alertmanager 0.34.1. The synthetic webhook
accepted the firing offline-partition alert during full broker loss and the
resolved alert with the same fingerprint after all brokers returned. The
wrapper exited successfully and removed its containers and private network.

## Evidence required for deployment

The six processes share one Docker host and storage failure domain. Platform
owners must supply the target cluster/topic IDs, partition and replica
placements, physical failure domains, advertised listeners, version/feature
levels, credential rotation, retention budgets, monitoring and recovery
targets. Record the observed KRaft quorum, ISR, group positions, outbox backlog
and remaining headroom before and after an actual failure-domain exercise.
Consumer owners approve the retained history and source incarnation. Keep the
target provisioning identity separate from all runtime principals, preserve
group positions and obtain the applicable change approval before any actual
topology, ACL, retention or offset change.

Kafka group read authority permits commits to the authorized group. Restrictions
on replay selection and the next committable numeric position also depend on
the admission runtime's generation fencing and the administrative boundary
around credentials. Restore and topic recreation require independent broker,
database and consumer evidence. [Kafka topology ADR](../adr/adr-07-kafka-topology.md)
