package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"hello/internal/config"
	"hello/internal/database"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/kafka"
)

func main() {
	cfg := config.LoadSchedulerConfig()
	ctx := context.Background()

	// Connect database
	db, err := database.New(ctx, cfg.GetDatabaseURL())
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer db.Close()

	queries := sqlc.New(db)

	// Initialize repository and producer
	notifRepo := repository.NewNotificationRepository(queries)
	producer := kafka.NewProducer(cfg.KafkaBroker, cfg.SendTopic)

	// Parse scheduler interval
	interval, err := time.ParseDuration(cfg.SchedulerInterval)
	if err != nil {
		log.Fatalf("Invalid SCHEDULER_INTERVAL: %v", err)
	}

	log.Printf("Scheduler started. Checking every %s", interval)

	// Create ticker
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Run immediately on start
	processScheduled(ctx, notifRepo, producer)

	go func() {
		for range ticker.C {
			processScheduled(ctx, notifRepo, producer)
		}
	}()

	<-sigCh
	log.Println("Shutting down scheduler...")
}

// processScheduled fetches due scheduled notifications and queues them to Kafka.
func processScheduled(ctx context.Context, repo *repository.NotificationRepository, producer *kafka.Producer) {
	log.Println("[Scheduler] Checking for scheduled notifications...")

	rows, err := repo.ListScheduledNotificationsDue(ctx, 100)
	if err != nil {
		log.Printf("[Scheduler] Failed to fetch scheduled notifications: %v", err)
		return
	}

	if len(rows) == 0 {
		return
	}

	log.Printf("[Scheduler] Found %d scheduled notifications due", len(rows))

	for _, notif := range rows {
		// Atomic update: only if status is still SCHEDULED
		rowsAffected, err := repo.UpdateStatusIfScheduled(ctx, notif.ID, "QUEUED")
		if err != nil {
			log.Printf("[Scheduler] Failed to update notification %d: %v", notif.ID, err)
			continue
		}
		if rowsAffected == 0 {
			// Already processed by another instance
			log.Printf("[Scheduler] Notification %d already processed, skipping", notif.ID)
			continue
		}

		// Publish to Kafka
		event := kafka.NotificationSendRequested{
			NotificationID: notif.ID,
			RequestedAt:    time.Now(),
		}
		if err := producer.PublishSendRequested(ctx, event); err != nil {
			log.Printf("[Scheduler] Failed to publish notification %d: %v", notif.ID, err)
			// Rollback to SCHEDULED
			_, _ = repo.UpdateStatusIfScheduled(ctx, notif.ID, "SCHEDULED")
			continue
		}

		log.Printf("[Scheduler] Queued notification %d (scheduled at %s)", notif.ID, notif.ScheduledAt.Time)
	}
}
