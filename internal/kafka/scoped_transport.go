package kafka

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode"

	broker "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
)

var (
	ErrScopedTransport = errors.New("invalid authenticated Kafka transport")
	ErrScopedSource    = errors.New("Kafka source verification failed")
)

// ScopedProducerConfig supplies immutable routing and authenticated TLS policy.
// TLS certificates, private keys and SASL implementations must remain immutable
// after construction. The producer copies routing and clones the TLS config.
type ScopedProducerConfig struct {
	Brokers []string
	Topic   string
	TLS     *tls.Config
	SASL    sasl.Mechanism
}

// Validate performs no I/O. It requires verified TLS 1.2+ with either client
// certificates or SASL; plaintext and TLS server-auth-only profiles are rejected.
func (c ScopedProducerConfig) Validate() error {
	if len(c.Brokers) < 1 || len(c.Brokers) > 8 || c.TLS == nil || c.TLS.InsecureSkipVerify || c.TLS.MinVersion < tls.VersionTLS12 ||
		(c.TLS.MaxVersion != 0 && c.TLS.MaxVersion < c.TLS.MinVersion) || (len(c.TLS.Certificates) == 0 && c.SASL == nil) {
		return ErrScopedTransport
	}
	if len(c.Topic) < 1 || len(c.Topic) > 249 || c.Topic == "." || c.Topic == ".." || strings.ContainsFunc(c.Topic, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-')
	}) {
		return ErrScopedTransport
	}
	seen := make(map[string]bool, len(c.Brokers))
	for _, address := range c.Brokers {
		host, port, err := net.SplitHostPort(address)
		number, portErr := strconv.ParseUint(port, 10, 16)
		if err != nil || host == "" || portErr != nil || number == 0 || strings.ContainsFunc(address, unicode.IsSpace) || seen[address] {
			return ErrScopedTransport
		}
		seen[address] = true
	}
	return nil
}

// NewAuthenticatedScopedProducer owns its transport and writer after successful
// validation. Call Close after stopping new calls, even after cancellation; a
// timed-out WriteMessages call may retain producer-owned I/O until drain finishes.
func NewAuthenticatedScopedProducer(cfg ScopedProducerConfig) (*ScopedProducer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	tlsConfig := cfg.TLS.Clone()
	if tlsConfig.RootCAs != nil {
		tlsConfig.RootCAs = tlsConfig.RootCAs.Clone()
	}
	transport := &broker.Transport{DialTimeout: time.Second, TLS: tlsConfig, SASL: cfg.SASL, MetadataTopics: []string{cfg.Topic}, ClientID: "hello-scoped-publisher"}
	return newScopedProducer(append([]string(nil), cfg.Brokers...), cfg.Topic, transport), nil
}

// VerifyClusterTopic checks live cluster/topic/partition metadata through the
// same transport used for publication. It does not prove a topic incarnation:
// the pinned SDK omits topic UUIDs, so a reviewed deployment manifest and safe
// handoff are still required. Errors contain no broker diagnostics.
func (p *ScopedProducer) VerifyClusterTopic(ctx context.Context, cluster string, partitions int32) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if cluster == "" || partitions < 1 {
		return ErrScopedSource
	}
	client := &broker.Client{Addr: p.writer.Addr, Timeout: time.Second, Transport: p.transport}
	metadata, err := client.Metadata(ctx, &broker.MetadataRequest{Topics: []string{p.writer.Topic}})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrScopedSource
	}
	return verifyScopedMetadata(metadata, cluster, p.writer.Topic, partitions)
}

func verifyScopedMetadata(metadata *broker.MetadataResponse, cluster, topicName string, count int32) error {
	if metadata == nil || metadata.ClusterID != cluster || len(metadata.Topics) != 1 || count < 1 {
		return ErrScopedSource
	}
	topic := metadata.Topics[0]
	if topic.Name != topicName || topic.Error != nil || topic.Internal || int64(len(topic.Partitions)) != int64(count) {
		return ErrScopedSource
	}
	seen := make(map[int]bool, len(topic.Partitions))
	for _, partition := range topic.Partitions {
		if partition.Error != nil || partition.ID < 0 || int64(partition.ID) >= int64(count) || seen[partition.ID] ||
			partition.Leader.Host == "" || partition.Leader.Port < 1 || partition.Leader.Port > 65535 {
			return ErrScopedSource
		}
		seen[partition.ID] = true
	}
	return nil
}
