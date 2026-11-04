# M05 generation-bound Kafka admission adapter

This adapter owns its ConsumerGroup configuration and runs one SerialConsumer
per generation. CommitOffsets uses that exact Generation's group/member/ID,
not a callback resolved against a later assignment. Acknowledgement commits only
this prepared/quarantined message's partition at offset+1. Any ack error ends
the run; there is no later fetch. Generation cancellation propagates to database
preparation/quarantine and fetchers, which are joined before generation exit.

The group has no automatic offset commit. Explicit synchronous coordinator calls
have a one-second network timeout; acknowledgement checks cancellation before
and after the request and reserves that network bound within its call deadline.
Coordinator metadata/dial operations are bounded. The caller owns retry/backoff
of a failed overall run. No provider is called and no binary is activated.

Assigned partitions have bounded readers (queue capacity 1, fetch byte target
1 MiB) and a bounded merge channel. Oversized records stop the run before copying
or acknowledgement. Kafka can return a first record larger than its fetch byte
target; broker/topic message-size policy is required to bound network allocation.
At most 16 partitions per member are accepted
by this implementation; this is a memory/input limit, not a production topology
recommendation. Fetching may prebuffer partition records, but only one message
enters admission and acknowledgement at a time. Partition fetch errors stop the
generation/run rather than acknowledge buffered work out of order.

Deployment supplies an explicit registered physical source ID, cluster identity,
topic and verified topic-incarnation generation. Registration must be active and
the deployment unsealed. Startup/assignment verification matches the immutable
database source and broker cluster/topic/assigned partitions. Current source
approval is rechecked before handing a message to admission.

Each database source-approval check has a five-second budget independent of the
generation's potentially long lifetime, while preserving any earlier caller
deadline. Idle message waiting still follows generation/caller cancellation;
this database limit is not an idle-reader timeout.

Pinned kafka-go v0.4.51 exposes Metadata v0–8 and does not return Kafka's topic
UUID. Therefore the incarnation generation is an operator-verified manifest,
not automatic detection of topic recreation. Production requires provisioning
and ACL/runbook controls that replace the manifest/source identity when a topic
is recreated; an unknown/unreviewed generation must remain inactive. This gate
must not be described as automatic topic-UUID validation. TLS/SASL and deployment
shutdown timing remain explicit production configuration work.

Local tests must exercise actual broker generation cancellation, no unsafe offset
advance and redelivery/dedup before process wiring. Source:
[pinned ConsumerGroup](https://github.com/segmentio/kafka-go/blob/v0.4.51/consumergroup.go)
and [metadata protocol](https://github.com/segmentio/kafka-go/blob/v0.4.51/protocol/metadata/metadata.go).

Local evidence: TestKafkaAdmissionGenerationHandoff creates a dedicated two-partition
topic and two members. The first preparation is suspended until actual generation
revocation; its partition remains uncommitted. Subsequent admission uses the real
PostgreSQL Preparer, records two complete snapshots with exactly two binding
intents, and commits both partitions. A malformed binary-key message retains one
quarantine row before its offset advances. Both members join their fetchers on
shutdown. The complete integration suite passes with GOFLAGS=-race. This uses one
local broker, synthetic mobile identity and an explicitly provisioned fixture
incarnation; it proves neither production Kafka HA nor real FCM delivery.
