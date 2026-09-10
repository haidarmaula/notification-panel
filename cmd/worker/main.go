// Package main implements the notification delivery worker for the
// Notification Panel.
//
// The worker consumes NotificationSendRequested events from the configured
// send topic, resolves each notification's target audience (a segment or an
// explicit user list) against PostgreSQL, dispatches push notifications
// through the configured provider (OneSignal by default), persists per-device
// delivery outcomes, and emits NotificationDeliveryUpdated events on the
// update topic for downstream analytics consumers.
//
// # Scaling model
//
// The worker is designed to run as a horizontally scalable Kafka consumer
// group. Multiple instances sharing the same GroupID partition the send topic
// between them, so throughput is increased by adding replicas rather than by
// changing code.
//
// # Delivery semantics
//
// Offsets are committed only after the processor reports success, yielding
// at-least-once semantics: a crash between processing and commit causes Kafka
// to redeliver the message. Processors MUST therefore be idempotent (see
// notifications.Processor.ProcessSendRequested).
//
// # Configuration and lifecycle
//
// Configuration is loaded from the environment via config.LoadWorkerConfig.
// The process shuts down gracefully on SIGINT/SIGTERM.
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"hello/internal/config"
	"hello/internal/database"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/features/notifications"
	"hello/internal/kafka"

	kafkago "github.com/segmentio/kafka-go"
)

// main runs the Kafka consumer worker that processes notification send
// requests until the process receives SIGINT or SIGTERM.
//
// # Startup sequence
//
//  1. Load worker configuration and open a PostgreSQL connection pool. A
//     failure to connect is fatal — the worker cannot operate without a
//     backing database.
//  2. Construct the repositories, push provider, Kafka producer, and the
//     notifications.Processor that orchestrates the delivery pipeline.
//  3. Create a kafka-go Reader bound to the send topic and the shared
//     consumer GroupID "notification-worker-group", so multiple worker
//     replicas divide partitions instead of duplicating work.
//  4. Run the fetch/process/commit loop in a background goroutine while the
//     main goroutine blocks on SIGINT/SIGTERM for graceful shutdown.
//
// # Error handling policy
//
//   - A JSON decode failure is treated as a poison message: the offset is
//     committed so the consumer does not stall on a permanently-bad payload,
//     and the event is dropped.
//   - A processor failure is logged and the offset is NOT committed, allowing
//     Kafka to redeliver the message to the group (at-least-once semantics).
//   - A fetch error is logged and the loop continues, providing resilience to
//     transient broker issues.
//
// # Concurrency
//
// main owns the signal channel; the consumer loop runs on a dedicated
// goroutine. Only that goroutine touches the reader, so no additional
// synchronisation is required. The processor is only invoked from that single
// goroutine, so its dependencies do not need to be internally goroutine-safe
// for the current call pattern — but they are typically safe regardless
// because pgx pools and kafka-go writers are designed for concurrent use.
func main() {
	cfg := config.LoadWorkerConfig()
	ctx := context.Background()

	db, err := database.New(ctx, cfg.GetDatabaseURL())
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer db.Close()

	queries := sqlc.New(db)

	// Initialize repositories.
	notifRepo := repository.NewNotificationRepository(queries)
	targetRepo := repository.NewNotificationTargetRepository(queries)
	deliveryRepo := repository.NewNotificationDeliveryRepository(queries)
	deviceRepo := repository.NewDeviceTokenRepository(queries)
	userRepo := repository.NewUserRepository(queries)
	segmentRepo := repository.NewSegmentRepository(queries)
	memberRepo := repository.NewSegmentMemberRepository(queries)

	// Use OneSignal as the push provider.
	provider := notifications.NewOneSignalProvider(cfg.OneSignalAppID, cfg.OneSignalAPIKey)
	producer := kafka.NewProducer(cfg.KafkaBroker, cfg.UpdateTopic)

	processor := notifications.NewProcessor(
		notifRepo, targetRepo, deliveryRepo, deviceRepo,
		userRepo, segmentRepo, memberRepo,
		provider, producer,
	)

	// Kafka consumer configuration.
	consumer := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  []string{cfg.KafkaBroker},
		Topic:    cfg.SendTopic,
		GroupID:  "notification-worker-group",
		MinBytes: 10e3,
		MaxBytes: 10e6,
		MaxWait:  1 * time.Second,
	})
	defer consumer.Close()

	log.Printf("Worker started. Listening on topic: %s", cfg.SendTopic)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for {
			msg, err := consumer.FetchMessage(ctx)
			if err != nil {
				log.Printf("Error fetching message: %v", err)
				continue
			}

			var event kafka.NotificationSendRequested
			if err := json.Unmarshal(msg.Value, &event); err != nil {
				log.Printf("Failed to unmarshal event: %v", err)
				_ = consumer.CommitMessages(ctx, msg)
				continue
			}

			if err := processor.ProcessSendRequested(ctx, event); err != nil {
				log.Printf("Failed to process notification %d: %v", event.NotificationID, err)
				continue
			}

			if err := consumer.CommitMessages(ctx, msg); err != nil {
				log.Printf("Failed to commit offset: %v", err)
			}
		}
	}()

	<-sigCh
	log.Println("Shutting down worker...")
}
