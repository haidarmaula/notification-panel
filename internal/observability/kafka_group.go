package observability

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"hello/internal/kafka"
)

type kafkaGroupCollector struct {
	read        func(context.Context) ([]kafka.GroupLagPartition, error)
	mu          sync.RWMutex
	partitions  []kafka.GroupLagPartition
	lastSuccess int64
	healthy     *prometheus.Desc
	timestamp   *prometheus.Desc
	valid       *prometheus.Desc
	lag         *prometheus.Desc
}

// SetKafkaGroupReader installs the admission checkpoint sampler. Call once
// before Run. The reader must honor cancellation and return an owned sample.
func (s *State) SetKafkaGroupReader(read func(context.Context) ([]kafka.GroupLagPartition, error)) {
	partition := []string{"partition"}
	c := &kafkaGroupCollector{
		read: read,
		healthy: prometheus.NewDesc("hello_kafka_group_sample_healthy",
			"One when the latest Kafka checkpoint sample completed.", nil, nil),
		timestamp: prometheus.NewDesc("hello_kafka_group_sample_timestamp_seconds",
			"Unix time of the last successful Kafka checkpoint sample.", nil, nil),
		valid: prometheus.NewDesc("hello_kafka_offset_range_valid",
			"One when the committed checkpoint lies within the sampled log bounds.", partition, nil),
		lag: prometheus.NewDesc("hello_kafka_group_lag_offsets",
			"Distance from a valid committed checkpoint to the sampled readable end.", partition, nil),
	}
	s.registry.MustRegister(c)
	s.kafkaGroup = c
}

func (c *kafkaGroupCollector) run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		c.sample(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *kafkaGroupCollector) sample(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	partitions, err := c.read(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil || ctx.Err() != nil || len(partitions) == 0 {
		c.partitions = nil
		return
	}
	c.lastSuccess = time.Now().Unix()
	c.partitions = partitions
}

func (c *kafkaGroupCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.healthy
	ch <- c.timestamp
	ch <- c.valid
	ch <- c.lag
}

func (c *kafkaGroupCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	partitions, lastSuccess := c.partitions, c.lastSuccess
	c.mu.RUnlock()
	ch <- prometheus.MustNewConstMetric(c.healthy, prometheus.GaugeValue, gauge(len(partitions) > 0))
	ch <- prometheus.MustNewConstMetric(c.timestamp, prometheus.GaugeValue, float64(lastSuccess))
	for _, p := range partitions {
		id := strconv.Itoa(p.Partition)
		ch <- prometheus.MustNewConstMetric(c.valid, prometheus.GaugeValue, gauge(p.Valid), id)
		if p.Valid {
			ch <- prometheus.MustNewConstMetric(c.lag, prometheus.GaugeValue, float64(p.Lag), id)
		}
	}
}
