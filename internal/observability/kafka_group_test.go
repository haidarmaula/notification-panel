package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"hello/internal/kafka"
)

func TestKafkaGroupScrapeOmitsLagWithoutValidCheckpoint(t *testing.T) {
	state := New("admission")
	scrape := func() string {
		w := httptest.NewRecorder()
		state.handlers().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		require.Equal(t, http.StatusOK, w.Code)
		return w.Body.String()
	}
	require.NotContains(t, scrape(), "hello_kafka_group")
	var readErr error
	state.SetKafkaGroupReader(func(context.Context) ([]kafka.GroupLagPartition, error) {
		return []kafka.GroupLagPartition{{Partition: 0, Valid: true}, {Partition: 1, Valid: true, Lag: 7}, {Partition: 2}}, readErr
	})
	require.Contains(t, scrape(), "hello_kafka_group_sample_healthy 0")
	state.kafkaGroup.sample(t.Context())
	body := scrape()
	require.Contains(t, body, "hello_kafka_group_sample_healthy 1")
	require.Contains(t, body, `hello_kafka_group_lag_offsets{partition="0"} 0`)
	require.Contains(t, body, `hello_kafka_group_lag_offsets{partition="1"} 7`)
	require.Contains(t, body, `hello_kafka_offset_range_valid{partition="2"} 0`)
	require.NotContains(t, body, `hello_kafka_group_lag_offsets{partition="2"}`)
	lastSuccess := state.kafkaGroup.lastSuccess
	require.Positive(t, lastSuccess)

	readErr = errors.New("fixture broker unavailable")
	state.kafkaGroup.sample(t.Context())
	body = scrape()
	require.Contains(t, body, "hello_kafka_group_sample_healthy 0")
	require.NotContains(t, body, "hello_kafka_group_lag_offsets")
	require.NotContains(t, body, "hello_kafka_offset_range_valid")
	require.Equal(t, lastSuccess, state.kafkaGroup.lastSuccess)
	require.NotContains(t, body, readErr.Error())
}
