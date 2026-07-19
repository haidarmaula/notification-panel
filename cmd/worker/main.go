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

func main() {
	cfg := config.Load()
	ctx := context.Background()

	// // Decode FCM credentials
	// fcmCreds, err := cfg.GetFCMCredentialsBytes()
	// if err != nil {
	// 	log.Fatal("Failed to decode FCM credentials:", err)
	// }
	//
	// // Initialize FCM provider
	// provider, err := notifications.NewFCMProvider(fcmCreds)
	// if err != nil {
	// 	log.Fatal("Failed to initialize FCM provider:", err)
	// }

	db, err := database.New(ctx, cfg.GetDatabaseURL())
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer db.Close()

	queries := sqlc.New(db)

	notifRepo := repository.NewNotificationRepository(queries)
	targetRepo := repository.NewNotificationTargetRepository(queries)
	deliveryRepo := repository.NewNotificationDeliveryRepository(queries)
	deviceRepo := repository.NewDeviceTokenRepository(queries)
	userRepo := repository.NewUserRepository(queries)
	segmentRepo := repository.NewSegmentRepository(queries)
	memberRepo := repository.NewSegmentMemberRepository(queries)

	// provider := notifications.NewMockProvider()
	provider := notifications.NewOneSignalProvider(cfg.OneSignalAppID, cfg.OneSignalAPIKey)
	producer := kafka.NewProducer(cfg.KafkaBroker, cfg.UpdateTopic)

	processor := notifications.NewProcessor(
		notifRepo,
		targetRepo,
		deliveryRepo,
		deviceRepo,
		userRepo,
		segmentRepo,
		memberRepo,
		provider,
		producer,
	)

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
