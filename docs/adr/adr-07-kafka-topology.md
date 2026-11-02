# ADR-07 — Self-hosted Kafka topology, provisioning and recovery boundaries

**Status:** proposed, 2026-11-02. Design only; not provisioned, benchmarked or
accepted for production. NEW companion SPEC: `docs/specs/adr-07-kafka-topology.md`
(not yet written). Evidence and operator gates:
[Kafka readiness](../analysis/kafka-readiness.md).

**Scope:** Kafka deployment/topic configuration, provisioning artifacts, broker
client config/producer/reader, operator access, runbook and verification. Current
docker-compose.yml remains local development; no infrastructure changes here.


Cross-ADR protocol: [consistency review](../analysis/adr-01-11-consistency-review.md).
Its shared lock/identity rules and verification gates apply to this proposal;
review completion does not mean acceptance or runtime implementation.

## Confirmed requirements and evidence

User confirms Kafka will be deployed on company-owned servers: **self-hosted**.
Number of independent servers/storage/network failure domains and the operations
team are unknown. Approximately 20,000 active devices, five campaigns/month,
FCM broadcast to eligible active users. Device count is not partition count.

Current compose pins apache/kafka:4.3.1, one combined broker/controller, RF=1
for internal topics and plaintext listeners. Worker group is hardcoded
notification-worker-group. NewProducer uses RequireAll, no automatic topic
creation, **LeastBytes** and default batching. No provisioning script/manifest
was found (root Makefile and tracked YAML inspected).

Pinned kafka-go v0.4.51 balancer.go proves LeastBytes does not hash keys. A local
dependency-only probe with the same key six times over partitions 0/1/2 returned
LeastBytes [0 1 2 0 1 2], Hash [2 2 2 2 2 2]. No broker or provider was contacted.
The current producer Godoc/AGENTS partition-affinity claim is therefore stale.
Confirmed open runtime findings H47/H48/M48 are recorded in code-review.md.

## Dependencies

[ADR-01](adr-01-provider-retry.md) owns DB delivery intent, lease, rate limits
and recovery, not Kafka partition-based provider concurrency.
[ADR-02](adr-02-worker-consumption.md) owns serial safe admission/offset commit.
[ADR-03](adr-03-transactional-outbox.md) and
[ADR-05](adr-05-receipt-ingestion.md) own send/update outboxes.
[ADR-06](adr-06-onesignal-fcm-cutover.md) gates cutover. NEW ADR-08/10/11 define
metrics/alerts, data retention/DR and security. Kafka is not the only source
of truth and is not a substitute for PostgreSQL backup or provider dedup.

## A-1. Separate development and production availability profiles

### Context

RequireAll with RF=1 does not survive that sole broker/server being lost.
Several containers on one physical server do not create independent failure domains.

### Decision

Keep a one-node development profile with RF=1/minISR=1 and explicit non-HA
label. Add a production-like integration profile with multiple partitions and
three replicas for failure tests; it is not evidence of independent physical
hosts if all test containers share one machine.

Proposed production baseline: three data brokers across three independent host/
storage failure domains, rack-aware replica placement, and a three-voter KRaft
controller quorum with reviewed placement and resource isolation. Prefer
separate controller processes from broker processes; dedicated controller
capacity is reviewed with ops. Six processes/VMs do not necessarily require
six physical hosts, but quorum and replicas must tolerate losing one actual
host without losing a majority. Do not count VMs sharing a sole hypervisor/
storage device as independent merely because IPs differ.

Use vetted, pinned broker/client versions and test their protocol/features;
compose's 4.3.1 is an inventory fact, not a security approval for production.
No ZooKeeper migration or unmanaged version upgrade is assumed. If only one
company server is available, record a single-host non-HA option and the reduced
availability explicitly; it cannot claim the HA baseline or its failover gate.
Do not silently reduce replication/minISR to make an undersized cluster start.

### Alternatives rejected

Three containers on one host cannot survive host loss. Adding brokers solely
for device count is not capacity planning. Replacing Kafka with another queue
is outside this increment and requires its own architecture decision.

### Consequences

Host topology, disks, network, patching/on-call and recovery targets remain
production gates. This ADR records the desired failure tolerance, not a server
purchase, provisioning action or established SLO.

## A-2. Explicit topics and durability contract

### Context

The two topics have very different volumes: send events are campaign-level;
updates are per-device facts. Key-based log compaction would drop update history.

### Decision

Initial production candidates (not actual deployed settings):

| Logical topic | Base name | Partitions | RF | min.insync.replicas | cleanup.policy |
| --- | --- | --- | --- | --- | --- |
| Send requests | notification.send.requested | 3 | 3 | 2 | delete |
| Delivery updates | notification.delivery.updated | 3 | 3 | 2 | delete |

Keep base names for an isolated environment. If environments share a cluster,
use explicit environment/project prefixes and separate groups/ACLs; provision
and migrate resolved names rather than silently renaming existing topics.
No retry or DLQ topic is introduced: retries/poison evidence are DB-owned.
Schema-v2 compatibility follows ADR-05; an extra versioned topic is created only
after consumer review demonstrates that a safe same-topic rollout is impossible.

Producer RequireAll/acks=-1 plus RF3/minISR2 favors refusing unsafe writes when
too few replicas are in sync. Disable unclean leader election in the baseline.
Validate broker-version/Eligible Leader Replicas feature behavior explicitly;
do not treat an unreviewed feature's ISR/election semantics as the classic
baseline. Internal __consumer_offsets must also have appropriate replication,
ISR and healthy placement; application topic RF alone does not protect commits.
Review transaction-state topics if transactional clients are introduced; this
design does not assume Kafka transactions atomically include PostgreSQL/FCM.

No claim of zero data loss under correlated disk loss, controller quorum loss,
retention expiry or every storage failure. One-broker loss must be tested with
remaining replicas healthy; fewer than minISR retains outbox rows instead of
being bypassed by weaker acknowledgements.

### Alternatives rejected

RF1 + RequireAll is not HA. A compacted update topic keyed by notification ID
can erase different devices and transitions. Automatically creating typo topics
can send work where no consumer is listening.

### Consequences

Partition and replica counts are independent. Single campaign updates may
remain a hot partition; test that case rather than assume threefold throughput.
Once created, partition changes require A-3's migration review.

## A-3. Real key partitioning and controlled mapping changes

### Context

Deterministic key bytes are necessary but insufficient with the existing
LeastBytes balancer. Changing the number/selection of partitions changes mapping.

### Decision

Replace LeastBytes with explicitly configured kafka.Hash from the pinned
client (default FNV-1a behavior, verified by golden vectors). Keep key bytes
notification_<decimal id> and delivery_<decimal id> unchanged. All producers,
including requeue/dispatchers, share this contract; reject missing key in these
paths. Other-language writers must match the exact mapping, not assume their
default partitioner is equivalent. Preserve partition selection behavior when
a leader is unavailable: do not remap a key to another writable subset merely
to bypass the failure. Test the chosen writer/version against broker failures.

Same-key affinity is conditional on fixed partition topology/algorithm. It
does not make DB recovery serial, order two topics together, order concurrent
producers causally, or provide external exactly-once. ADR-05's delivery version
and outbox predecessor guard remain mandatory.

Balancer correction itself is a mapping migration for existing multi-partition
topics. Inventory actual cluster/topics/producers/consumers, hold/drain affected
publishers, capture boundary offsets and reconcile retained old events; enable
new writers only under a reviewed deployment plan. Old LeastBytes records
remain distributed; switching cannot retroactively reorder them. Partition
expansion follows the same review and consumer compatibility/replay plan.

### Alternatives rejected

Leaving LeastBytes while promising keyed ordering is incorrect. Blind expansion
or mixing partitioners can move the same key across partitions. Renaming key
prefixes is not needed to fix the balancer and breaks existing contracts.

### Consequences

H47 remains open until runtime code/tests are fixed. Documentation records
actual behavior now. Serial admission/device generation fences protect replay
while mapping changes are staged; they do not reconstruct missing old history.

## A-4. Retention, offsets and bounded recovery claims

### Context

Kafka deletes by retention independently of consumer progress; offsets and
message logs have separate lifetimes. A sparse campaign workload does not
justify deleting consumer state when workers are intentionally stopped.

### Decision

Proposed topic retention.ms=604800000 (7 days), cleanup.policy=delete,
message.timestamp.type=LogAppendTime, retention.bytes=-1 (time-based baseline).
Payload occurred_at remains immutable server event time; Kafka append time
must not be mistaken for business occurrence time. No compaction. Time-based
retention is segment-granular, not an exact erasure deadline. Size caps, if
required, can expire data earlier and must be capacity-reviewed explicitly.

Choose offsets.retention.minutes=43200 (30 days) as a proposed cluster policy
for empty/inactive groups, subject to ops review. It does not extend the 7-day
event window. Keep consumer dedup/projection versions at least through the
approved replay window. Final values must exceed agreed outage + detection +
repair + catch-up margin and fit disk/retention policy; 7/30 days are proposals,
not verified business requirements or guaranteed retention floors.

Capacity model includes peak event sizes, full broadcast concentration in one
partition, per-device submission/observation events, retries/replay, replication,
indexes/segment overhead and disk headroom. No 20,000-device payload in the
send topic. Application event cap is proposed 64KiB, with broker/producer/fetch
limits aligned including record overhead; error snippets remain bounded and
credentials/addresses/capabilities excluded. Monitor disk and age-to-retention
margin; do not silently purge unpublished DB outbox to recover disk space.

Before starting/restarting a group compare committed offsets with log start/
end and known deployment identity. Detect absent/expired/out-of-range positions;
do not silently choose latest to skip backlog. For a deliberately NEW group,
review start-at-earliest retained data and reconciliation consequences before
enablement. Runtime range loss must raise an alert/hold and reconciliation
workflow, not invisible progress. Pinned Reader auto-reset behavior must be
tested/instrumented; StartOffset alone is not a fail-on-offset-loss switch.

Past retention, DB intent/recovery can finalize expired campaigns or recover
eligible queued work, but deleted event history/poison bytes cannot be promised
back. Analytics may rebuild a tagged current-state snapshot from retained DB
facts, not pretend to recover every original transition. PostgreSQL restore
and Kafka offsets/consumer versions need a coordinated ADR-10 DR plan.

### Alternatives rejected

Retention does not wait for lagging consumers. Offset expiry is not log expiry.
Reset-to-latest and blind group rename can silently skip/replay business work.
Replication is not backup and DB snapshots cannot undo external push sends.

### Consequences

Retention/DR/SLO owners must agree outage windows before production. Alerting
needs lag age, oldest retained boundary and DB backlog, not offset count alone.

## A-5. Consumer groups and producer latency/seed configuration

### Context

One logical admission group distributes partitions; provider drivers remain
DB-owned. Current seed string and batching defaults are unsuitable assumptions.

### Decision

All admission replicas in one environment share one explicit
KAFKA_CONSUMER_GROUP (current base notification-worker-group); each independent
analytics application has its own reviewed group on updates. A new unique
group per replica would repeat all events. Extra replicas beyond partition
count do not increase Kafka admission, though DB provider pool allocation is
separate and must still respect the aggregate rate budget.

Use explicit synchronous commits (CommitInterval=0), serial admission from
ADR-02, and reviewed rebalance/shutdown timeouts. Proposed MinBytes=1 and
MaxWait=100ms avoid the current 10KB low-volume fetch target delaying a lone
campaign. Bound reader prefetch queue/bytes; one application message in flight
does not mean the client/network has zero prefetch. Partition changes and
group lifecycle tests are required; no arbitrary consumer framework switch.

NEW KAFKA_BROKERS parses a validated trimmed list of host:port endpoints,
supports IPv6 correctly and passes kafka.TCP(brokers...) / Reader.Brokers slice.
Legacy KAFKA_BROKER is one endpoint only; setting both is a config error.
Never pass comma-joined text as one kafka.TCP argument or one Reader seed.
All advertised broker addresses, not just the bootstrap seed, must be reachable
from deployed containers. Configure TLS/SASL in both writer transport/reader
dialer and admin tools, under ADR-11. No localhost advertised to remote clients.

Outbox producers remain synchronous Async=false, RequireAll, auto-create=false.
Explicit BatchSize=1 for the current single-row ack/delete protocol prevents
the default 100-message/1s batching wait. Bound batch bytes, internal attempts,
network timeouts and total WriteMessages context within the outbox transaction
budget; an unconfirmed/cancelled publish may still duplicate externally, so
retain the outbox and stable identity. Writer defaults are not a measured
latency guarantee. Benchmark per-event ack path; a future batched publisher
must revise DB claiming/ordering and transaction bounds explicitly.

Pinned Writer.WriteMessages documents that context cancellation may return
before the message is physically published, and publication may still happen
later. Caller timeout is not a network abort guarantee. Keep payload buffers
immutable, bound underlying dial/auth/read/write/retry lifetimes, and preserve
the outbox on uncertainty. ADR-05 consumers reject stale versions even if a
late copy arrives after a newer event. Do not claim the one-row DB lock removes
all delayed duplicates once a connection or caller context is lost.

### Alternatives rejected

Java producer configuration names/defaults cannot be copied into kafka-go and
assumed effective. Writer retries/acks do not establish end-to-end idempotence.
Implicit one-second wait per serial update can dominate whole-broadcast lag.

### Consequences

H48/M48 stay open pending implementation. Client config expands in worker,
scheduler and operator tooling; API server has no Kafka role after ADR-03.

## A-6. Provisioning as reviewed, repeatable configuration

### Context

Current local compose supplies no topics, and every Make target includes .env.
Ad hoc UI topic creation cannot detect deployment drift reliably.

### Decision

NEW deploy/kafka/topics.yaml records explicit environment profiles, resolved
names, partitions, RF, ISR, cleanup/retention/timestamp/size policy and version.
NEW scripts/kafka/provision.sh (or reviewed equivalent tooling) has check/plan
as default, explicit create/apply modes, target cluster-ID/environment checks,
timeouts, redacted auth-file inputs and post-apply describe verification.
Exact CLI/tool versions/schema and paths are fixed in the companion SPEC.

An existing matching topic is a no-op. Drift produces a report/nonzero status,
not automatic delete/recreate, retention shrink, partition expansion or forced
RF reduction. Existing RF uses a reviewed reassignment plan, not only a config
edit. Fewer brokers/failure domains than the selected profile must fail the
preflight. Operators verify actual effective settings, ISR/placement and
controller/internal-topic health before enabling dispatch.

Provisioning is a deployment job with admin privileges, never runtime auto-create.
Runtime principals lack topic/ACL deletion/creation grants. Local onboarding
may invoke dev-only provisioning after broker health; never make ordinary
application startup mutate a production cluster. Keep Makefile's .env include;
tooling must not print or commit credential material.

### Alternatives rejected

--if-not-exists alone does not validate an existing topic's settings. Silently
falling back to RF1 hides missing infrastructure. A typo must not provision
a second unconsumed topic under application credentials.

### Consequences

This ADR does not create YAML/scripts or run a Kafka CLI. Desired profile and
actual cluster evidence are separate artifacts; step 12 completes design only.

## A-7. Private transport, least privilege and operations

### Context

Company-hosted does not make plaintext, shared admin credentials or exposed
Kafka UI a production trust boundary.

### Decision

Production uses private routed listeners, encrypted/authenticated client and
broker/controller transport, ACLs, and restricted operator access per ADR-11.
Do not expose broker/controller ports or an unauthenticated Kafka UI publicly.
Pin diagnostic UI/tool versions and prohibit screenshots/log dumps with secrets.

| Principal / process | Required Kafka capability | Not granted by default |
| --- | --- | --- |
| Scheduler send dispatcher | Write/describe resolved send topic | Read updates, create/delete topics, cluster admin |
| Worker admission | Read/describe send topic + its group | Alter groups/topics or reset offsets |
| Worker update dispatcher | Write/describe update topic | Admin/consumer impersonation |
| Operator requeue | Audited write send topic | Arbitrary production topic writes |
| Analytics app | Read/describe update topic + own group | Another app's group or send publication |
| API/bootstrap | None after outbox migration | Broker/network/admin credentials |
| Provisioning operator | Scoped reviewed admin operations | Application service reuse of admin credential |

Actual Kafka ACL resource/operation/cluster-ID permissions depend on pinned
client/admin calls and must be least-privilege tested, not inferred from this
intent matrix. Credential/certificate rotation must keep quorum and clients
healthy; no secret value belongs in topic manifests or API responses.

Monitor offline/under-replicated partitions, ISR changes, controller quorum,
disk/inodes, request latency/errors, consumer lag age/assignment, commit errors,
outbox age and admission failure. Recovery drills: one broker/host down, quorum
loss, insufficient ISR, unreachable advertised listener, auth rotation, disk
pressure and lost/out-of-range offsets. ADR-08 defines alert actions/readiness;
restart loops must not hide storage or quorum outages.

### Alternatives rejected

One shared admin user makes process isolation meaningless. Internal UI exposure
is still access to operational data and write controls. Liveness restart is
not a repair for quorum/storage loss.

### Consequences

Ops ownership, backup/restore maintenance windows, network/certificate/secret
management and numeric alert thresholds are required before release.

## A-8. Rollout, acceptance and open decisions

### Context

Both topology and client behavior differ from the current dev configuration.

### Decision

Implement after the acceptance/outbox/event contracts are specified. Provision
and verify cluster/topics/ACLs, test parsed multi-seed failover and hash vectors,
then rehearse old-to-new producer mapping and consumer version compatibility.
Never deploy a mixed balancer fleet or new group name as an unreviewed shortcut.
Use expand-compatible config/schema and preserve outbox/evidence on rollback.
Record exact versions, cluster ID, topic IDs, profiles, placement, offsets and
approvers in a redacted deployment evidence report.
ADR-02 log_source_id binds cluster and topic incarnation to source offsets.
Recreation/migration must update verified source identity before consuming;
same topic name is not proof of the same log. Never silently map unknown topic
identity to an old dead-letter namespace.

Before SPEC acceptance: host/failure-domain inventory, resource budget, KRaft
placement, broker/client version and feature level, environment isolation,
consumer inventory, accepted outage/replay windows, disk model, TLS/SASL/IAM
choice, operators/on-call and RPO/RTO. Tests must distinguish dependency-only
probe, single-host containers, real multi-host failure and actual throughput.

### Alternatives rejected

Passing topic creation or a one-broker smoke test does not establish production
durability. Adding partitions live cannot repair historical LeastBytes ordering.

### Consequences

Observability is defined in [ADR-08](adr-08-observability.md), also proposed;
runtime findings remain open until implemented and verified.
No server, topic, group, credential, offset or deployment was changed here.

## Primary sources and local verification

Checked 2026-11-02:
[Kafka 4.3 topic configuration](https://kafka.apache.org/43/configuration/topic-configs/),
[broker configuration](https://kafka.apache.org/43/configuration/broker-configs/),
[operations and partition expansion](https://kafka.apache.org/43/operations/basic-kafka-operations/),
[KRaft operations](https://kafka.apache.org/43/operations/kraft/).
Broker configuration guidance is not a claim that Java client knobs exist in
kafka-go. Local dependency references are github.com/segmentio/kafka-go@v0.4.51
balancer.go, writer.go, reader.go, address.go and dialer.go. No credential/.env
read, broker connection or Kafka/FCM live operation was performed.

## Design verification cases for the companion SPEC

| Input / condition | Expected observable result |
| --- | --- |
| Single-host containers selected as HA | Profile validation reports shared failure domain; no HA acceptance |
| Production profile with fewer than 3 brokers | Provision preflight fails; no RF downgrade |
| One healthy replica/host lost | Remaining quorum/ISR behavior meets reviewed availability, tested across real domains |
| ISR below configured minimum | Publish unconfirmed, outbox retained; no ack weakening |
| Same key across repeated/multiple producers | Corrected Hash maps consistently on fixed topology; golden vectors pass |
| Current LeastBytes fixture | Reproduces non-affinity and prevents claiming old code is fixed |
| Balancer or partition-count migration | Explicit pause/boundary/replay plan; old history not retroactively ordered |
| Bootstrap endpoint fails / other seed healthy | Parsed seed list and advertised listeners permit recovery |
| CSV passed as a single endpoint | Validation rejects; no misleading HA seed claim |
| Sparse single-row outbox publication | Explicit batch config avoids default batch wait; measured ack budget fits |
| Existing topic drift | Plan/check fails with diff; no delete/recreate/partition mutation |
| Message retained but group offsets absent | Operator-reviewed start/reconcile; no silent latest reset |
| Offset older than retained log | Alert/hold and DB/snapshot reconciliation; lost history not fabricated |
| Two replicas share admission group | One group assignment; no group-per-replica replay |
| Wrong environment/ACL principal | Fail closed, no unauthorized publish/read/admin mutation |
| Credential rotation / quorum outage | Bounded failures, retained intent, actionable alert and tested recovery |
'''
