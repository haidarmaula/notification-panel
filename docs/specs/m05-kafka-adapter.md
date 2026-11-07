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

Each assignment requires a numeric group checkpoint at or between the current
log start and end offsets. An absent or expired commit stops the run. Each fetch
addresses that numeric offset and checks the broker's retained bounds. Kafka
may return the prefix of a batch before the requested offset. Admission consumes
the contiguous suffix in this delete-only send log and stops on a hole or
out-of-range position before treating a later message as prepared or quarantined.

Assigned partitions each hold one fetched batch (1 MiB target) and share a
bounded merge channel. Oversized records stop the run before copying or
acknowledgement. Kafka can return a first record larger than its fetch byte
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
must not be described as automatic topic-UUID validation.

[Authenticated runtime composition](m04-m05-runtime-composition.md) now provides
verified TLS/mTLS or SASL policy for group, metadata and partition reads, with an
explicit partition count. Deployed callers must use
`NewAuthenticatedKafkaAdmission`; the original constructor remains for plaintext
fixtures. Per-process trusted configuration and deployment shutdown timing remain
open.

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


The same two-member handoff fixture now also passes through the actual mTLS
listener (`TestAuthenticatedKafkaAdmissionGenerationHandoff`), including all
revocation, offset, quarantine, deduplication and shutdown assertions above.
This verifies mTLS locally; it does not verify a deployed SASL profile or Kafka HA.

## Provision and reconcile group offsets

The admission runtime starts from committed broker offsets. Existing group
configurations continue when every assigned partition has a retained numeric
checkpoint. A new deployment, partition addition, returning group with expired
commits, or restored environment requires a recorded operator decision before
the worker starts. An absent commit alone cannot distinguish these cases.

1. Stop admission and replay publishers for the affected source. Confirm prior
   producers have drained, the group has no active members and the source
   registry entry matches the approved cluster, topic incarnation, partition
   count and deployment group. Keep the group/bootstrap operator privilege
   separate from the worker account.
2. Capture the group commits and broker start/end for every partition. For a
   deliberately new group, record its approved earliest retained offset per
   partition and reconcile any prior history already lost. For an existing or
   restored group, use durable preparation/quarantine evidence, authoritative
   external high-water records and the recovery manifest to resolve ambiguity.
   Hold affected work when a safe next position cannot be established.
3. Review the explicit numeric positions while the group is stopped. Use the
   Kafka administrative tooling pinned for the deployment to dry-run a reset
   from that reviewed partition/offset list, execute the same list, then fetch
   and compare each committed value against the approved positions and current
   broker bounds. A new-group decision grants one initialization only. Preserve
   existing commits for unaffected partitions and retain the reviewed evidence.
4. Start the worker and check preparation/quarantine counts, committed offsets
   and consumer lag. A new missing checkpoint, a position below log start or
   above log end, or a hole during live fetch stops the generation for
   reconciliation. Topic recreation still requires a new approved physical
   source even if all offsets happen to lie within numeric bounds.
