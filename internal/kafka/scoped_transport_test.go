package kafka

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"

	broker "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/plain"
)

func TestAuthenticatedScopedProducerRequiresVerifiedTLSAndIdentity(t *testing.T) {
	for _, tt := range []struct {
		name string
		tls  *tls.Config
		auth bool
	}{
		{"missing TLS", nil, true},
		{"unverified TLS", &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}, true},
		{"old TLS", &tls.Config{MinVersion: tls.VersionTLS10}, true},
		{"missing identity", &tls.Config{MinVersion: tls.VersionTLS12}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var config ScopedProducerConfig
			config.Brokers = []string{"localhost:9093"}
			config.Topic = "notification.send.requested"
			config.TLS = tt.tls
			if tt.auth {
				config.SASL = plain.Mechanism{Username: "operator", Password: "secret"}
			}
			producer, err := NewAuthenticatedScopedProducer(config)
			if producer != nil || !errors.Is(err, ErrScopedTransport) {
				t.Fatal("accepted unsafe transport")
			}
		})
	}
}

func TestAuthenticatedScopedProducerCopiesRoutingAndTLS(t *testing.T) {
	addresses := []string{"localhost:9093"}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	producer, err := NewAuthenticatedScopedProducer(ScopedProducerConfig{Brokers: addresses, Topic: "notification.send.requested", TLS: tlsConfig, SASL: plain.Mechanism{Username: "operator", Password: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	addresses[0] = "changed:1"
	tlsConfig.InsecureSkipVerify = true
	if producer.transport.TLS.InsecureSkipVerify || producer.writer.Addr.String() != "localhost:9093" || producer.writer.MaxAttempts != 1 || producer.writer.RequiredAcks != broker.RequireAll || producer.writer.AllowAutoTopicCreation {
		t.Fatal("mutable or unsafe publisher policy")
	}
}

func TestVerifyScopedMetadata(t *testing.T) {
	good := func() *broker.MetadataResponse {
		return &broker.MetadataResponse{ClusterID: "cluster-a", Topics: []broker.Topic{{Name: "send", Partitions: []broker.Partition{{Topic: "send", ID: 0, Leader: broker.Broker{Host: "broker", Port: 9093}}, {Topic: "send", ID: 1, Leader: broker.Broker{Host: "broker", Port: 9093}}}}}}
	}
	if err := verifyScopedMetadata(good(), "cluster-a", "send", 2); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		change func(*broker.MetadataResponse)
	}{
		{"cluster mismatch", func(m *broker.MetadataResponse) { m.ClusterID = "other" }},
		{"topic mismatch", func(m *broker.MetadataResponse) { m.Topics[0].Name = "other" }},
		{"topic error", func(m *broker.MetadataResponse) { m.Topics[0].Error = errors.New("private broker detail") }},
		{"partition error", func(m *broker.MetadataResponse) { m.Topics[0].Partitions[0].Error = errors.New("private detail") }},
		{"partition count", func(m *broker.MetadataResponse) { m.Topics[0].Partitions = m.Topics[0].Partitions[:1] }},
		{"duplicate partition", func(m *broker.MetadataResponse) { m.Topics[0].Partitions[1].ID = 0 }},
		{"partition gap", func(m *broker.MetadataResponse) { m.Topics[0].Partitions[1].ID = 3 }},
		{"no leader", func(m *broker.MetadataResponse) { m.Topics[0].Partitions[0].Leader.Host = "" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := good()
			tt.change(m)
			if !errors.Is(verifyScopedMetadata(m, "cluster-a", "send", 2), ErrScopedSource) {
				t.Fatal("accepted mismatched source")
			}
		})
	}
	if !errors.Is(verifyScopedMetadata(nil, "cluster-a", "send", 2), ErrScopedSource) {
		t.Fatal("accepted nil metadata")
	}
}

func TestVerifyClusterTopicHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := NewScopedProducer("localhost:1", "send")
	defer p.Close()
	if !errors.Is(p.VerifyClusterTopic(ctx, "cluster", 1), context.Canceled) {
		t.Fatal("ignored cancellation")
	}
}
