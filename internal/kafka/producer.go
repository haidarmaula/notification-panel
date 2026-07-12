package kafka

import (
	"context"
	"encoding/json"

	"github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafka.Writer
}

// NewProducer creates a new Kafka producer with the given broker and topic.
// For development, you can pass "localhost:9092" and "notification.send.requested".
func NewProducer(broker, topic string) *Producer {
	w := &kafka.Writer{
		Addr:                   kafka.TCP(broker),
		Topic:                  topic,
		Balancer:               &kafka.LeastBytes{},
		RequiredAcks:           kafka.RequireOne,
		AllowAutoTopicCreation: true,
	}
	return &Producer{writer: w}
}

// PublishSendRequested publishes a send requested event.
func (p *Producer) PublishSendRequested(ctx context.Context, event NotificationSendRequested) error {
	value, err := json.Marshal(event)
	if err != nil {
		return err
	}

	key := []byte("notification_" + string(rune(event.NotificationID)))
	msg := kafka.Message{
		Key:   key,
		Value: value,
	}

	return p.writer.WriteMessages(ctx, msg)
}

// PublishDeliveryUpdated publishes a delivery updated event.
func (p *Producer) PublishDeliveryUpdated(ctx context.Context, event DeliveryUpdated) error {
	value, err := json.Marshal(event)
	if err != nil {
		return err
	}

	key := []byte("delivery_" + string(rune(event.NotificationID)))
	msg := kafka.Message{
		Key:   key,
		Value: value,
	}

	return p.writer.WriteMessages(ctx, msg)
}

// Close closes the producer writer.
func (p *Producer) Close() error {
	return p.writer.Close()
}
