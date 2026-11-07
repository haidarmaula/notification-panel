package dispatch

import (
	"testing"

	broker "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
)

func TestAdmissionConsumesOnlyContiguousSuffix(t *testing.T) {
	position := broker.PartitionAssignment{ID: 2, Offset: 8}
	record := func(offset int64) *broker.Record {
		return &broker.Record{Offset: offset, Key: broker.NewBytes([]byte("key")), Value: broker.NewBytes([]byte("body"))}
	}
	msg, err := admissionRecord(record(6), "send", position, 7)
	require.NoError(t, err)
	require.Nil(t, msg)
	_, err = admissionRecord(record(7), "send", position, 7)
	require.ErrorIs(t, err, ErrAdmissionCheckpoint)
	_, err = admissionRecord(record(9), "send", position, 7)
	require.ErrorIs(t, err, ErrAdmissionCheckpoint)
	msg, err = admissionRecord(record(8), "send", position, 7)
	require.NoError(t, err)
	require.Equal(t, broker.Message{Topic: "send", Partition: 2, Offset: 8, Key: []byte("key"), Value: []byte("body")}, *msg)
}
