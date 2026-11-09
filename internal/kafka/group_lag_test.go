package kafka

import (
	"testing"

	broker "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/protocol/listoffsets"
	"github.com/stretchr/testify/require"
)

func TestParseGroupLagDistinguishesUnknownFromZero(t *testing.T) {
	for _, tc := range []struct {
		name      string
		committed int64
		valid     bool
		lag       int64
	}{
		{name: "lagged", committed: 12, valid: true, lag: 8},
		{name: "caught up", committed: 20, valid: true},
		{name: "missing", committed: -1},
		{name: "expired", committed: 9},
		{name: "ahead", committed: 21},
	} {
		t.Run(tc.name, func(t *testing.T) {
			positions := map[string][]broker.OffsetFetchPartition{"send": {{CommittedOffset: tc.committed}}}
			got, err := parseGroupLag("send", 1, positions, groupLagBounds())
			require.NoError(t, err)
			require.Equal(t, []GroupLagPartition{{Valid: tc.valid, Lag: tc.lag}}, got)
		})
	}
}

func TestParseGroupLagRejectsIncompleteOrUntrustworthyReplies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*broker.OffsetFetchPartition, *listoffsets.ResponseTopic)
	}{
		{name: "commit error", mutate: func(p *broker.OffsetFetchPartition, _ *listoffsets.ResponseTopic) {
			p.Error = broker.TopicAuthorizationFailed
		}},
		{name: "omitted boundary", mutate: func(_ *broker.OffsetFetchPartition, r *listoffsets.ResponseTopic) {
			r.Partitions = r.Partitions[:1]
		}},
		{name: "duplicate boundary", mutate: func(_ *broker.OffsetFetchPartition, r *listoffsets.ResponseTopic) {
			r.Partitions[1].Timestamp = broker.FirstOffset
		}},
		{name: "failed boundary", mutate: func(_ *broker.OffsetFetchPartition, r *listoffsets.ResponseTopic) {
			r.Partitions[1].ErrorCode = int16(broker.NotLeaderForPartition)
		}},
		{name: "inverted bounds", mutate: func(_ *broker.OffsetFetchPartition, r *listoffsets.ResponseTopic) {
			r.Partitions[1].Offset = 5
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			positions := map[string][]broker.OffsetFetchPartition{"send": {{CommittedOffset: 20}}}
			bounds := groupLagBounds()
			tc.mutate(&positions["send"][0], &bounds[0])
			got, err := parseGroupLag("send", 1, positions, bounds)
			require.ErrorIs(t, err, errGroupLagUnavailable)
			require.Nil(t, got)
		})
	}
}

func groupLagBounds() []listoffsets.ResponseTopic {
	return []listoffsets.ResponseTopic{{Topic: "send", Partitions: []listoffsets.ResponsePartition{
		{Timestamp: broker.FirstOffset, Offset: 10},
		{Timestamp: broker.LastOffset, Offset: 20},
	}}}
}
