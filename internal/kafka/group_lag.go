package kafka

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	broker "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/protocol/listoffsets"
)

var errGroupLagUnavailable = errors.New("Kafka group checkpoint sample unavailable")

// Bound both scrape cardinality and the ListOffsets requests per poll.
const maxGroupLagPartitions = 64

// GroupLagPartition reports the distance from a committed offset to the
// broker's sampled end offset. Lag is meaningful only when Valid is true.
type GroupLagPartition struct {
	Partition int
	Valid     bool
	Lag       int64
}

// GroupLagReader owns a read-only transport for one configured topic and group.
type GroupLagReader struct {
	client    *broker.Client
	transport *broker.Transport
	cluster   string
	topic     string
	group     string
	count     int32
}

// NewGroupLagReader requires the same authenticated transport as admission.
// It creates no consumer membership.
func NewGroupLagReader(cfg ScopedProducerConfig, cluster, group string, partitions int32) (*GroupLagReader, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !validGroupLagIdentity(cluster) || !validGroupLagIdentity(group) || partitions < 1 {
		return nil, ErrScopedSource
	}
	tlsConfig := cfg.TLS.Clone()
	if tlsConfig.RootCAs != nil {
		tlsConfig.RootCAs = tlsConfig.RootCAs.Clone()
	}
	transport := &broker.Transport{
		DialTimeout: time.Second, TLS: tlsConfig, SASL: cfg.SASL,
		MetadataTopics: []string{cfg.Topic}, ClientID: "hello-group-checkpoint-monitor",
	}
	return &GroupLagReader{
		client:    &broker.Client{Addr: broker.TCP(cfg.Brokers...), Timeout: 2 * time.Second, Transport: transport},
		transport: transport, cluster: cluster, topic: cfg.Topic, group: group, count: partitions,
	}, nil
}

func validGroupLagIdentity(value string) bool {
	return len(value) > 0 && len(value) <= 255 && utf8.ValidString(value) &&
		strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}

// Close releases idle connections after the owner has joined the sampler.
func (r *GroupLagReader) Close() { r.transport.CloseIdleConnections() }

// Read verifies topology and samples committed offsets before log bounds. The
// reads can observe concurrent retention or commits at different instants.
// A failed or malformed reply yields no sample. A missing or out-of-range
// committed offset has Valid=false and is never reported as zero lag.
func (r *GroupLagReader) Read(parent context.Context) ([]GroupLagPartition, error) {
	if r.count > maxGroupLagPartitions {
		return nil, errGroupLagUnavailable
	}
	ctx, cancel := context.WithTimeout(parent, 4*time.Second)
	defer cancel()
	unavailable := func() ([]GroupLagPartition, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, errGroupLagUnavailable
	}
	metadata, err := r.client.Metadata(ctx, &broker.MetadataRequest{Topics: []string{r.topic}})
	if err != nil || ValidateScopedMetadata(metadata, r.cluster, r.topic, r.count) != nil {
		return unavailable()
	}
	ids := make([]int, r.count)
	bounds := make([]listoffsets.RequestPartition, 0, 2*r.count)
	for i := range ids {
		ids[i] = i
		for _, timestamp := range []int64{broker.FirstOffset, broker.LastOffset} {
			bounds = append(bounds, listoffsets.RequestPartition{Partition: int32(i), CurrentLeaderEpoch: -1, Timestamp: timestamp})
		}
	}
	positions, err := r.client.OffsetFetch(ctx, &broker.OffsetFetchRequest{
		GroupID: r.group, Topics: map[string][]int{r.topic: ids},
	})
	if err != nil || positions.Error != nil {
		return unavailable()
	}
	// Use the raw response to distinguish an omitted broker boundary from
	// zero. Client.ListOffsets fills requested fields with zero before decoding.
	message, err := r.transport.RoundTrip(ctx, r.client.Addr, &listoffsets.Request{
		ReplicaID: -1, Topics: []listoffsets.RequestTopic{{Topic: r.topic, Partitions: bounds}},
	})
	if err != nil {
		return unavailable()
	}
	reply, ok := message.(*listoffsets.Response)
	if !ok {
		return unavailable()
	}
	return parseGroupLag(r.topic, len(ids), positions.Topics, reply.Topics)
}

func parseGroupLag(topic string, count int, positions map[string][]broker.OffsetFetchPartition, reply []listoffsets.ResponseTopic) ([]GroupLagPartition, error) {
	if len(positions) != 1 || len(positions[topic]) != count || len(reply) != 1 ||
		reply[0].Topic != topic || len(reply[0].Partitions) != 2*count {
		return nil, errGroupLagUnavailable
	}
	type partitionBounds struct {
		first, end       int64
		hasFirst, hasEnd bool
	}
	ranges := make([]partitionBounds, count)
	for _, row := range reply[0].Partitions {
		if row.Partition < 0 || int(row.Partition) >= count || row.ErrorCode != 0 || row.Offset < 0 {
			return nil, errGroupLagUnavailable
		}
		b := &ranges[row.Partition]
		switch {
		case row.Timestamp == broker.FirstOffset && !b.hasFirst:
			b.first, b.hasFirst = row.Offset, true
		case row.Timestamp == broker.LastOffset && !b.hasEnd:
			b.end, b.hasEnd = row.Offset, true
		default:
			return nil, errGroupLagUnavailable
		}
	}
	sample := make([]GroupLagPartition, count)
	seen := make([]bool, count)
	for _, position := range positions[topic] {
		id := position.Partition
		if id < 0 || id >= count || seen[id] || position.Error != nil {
			return nil, errGroupLagUnavailable
		}
		seen[id] = true
		b := ranges[id]
		if !b.hasFirst || !b.hasEnd || b.end < b.first {
			return nil, errGroupLagUnavailable
		}
		result := GroupLagPartition{Partition: id}
		if position.CommittedOffset >= b.first && position.CommittedOffset <= b.end {
			result.Valid, result.Lag = true, b.end-position.CommittedOffset
		}
		sample[id] = result
	}
	return sample, nil
}
