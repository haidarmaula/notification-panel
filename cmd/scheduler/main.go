// Package main implements the scheduled-notification processor for the
// Notification Panel.
//
// The scheduler periodically polls the notifications table for rows whose
// scheduled_at timestamp has elapsed and whose status is still SCHEDULED. For
// every due notification it performs an atomic state transition to QUEUED and
// publishes a NotificationSendRequested event to Kafka so the worker fan-out
// pipeline can pick it up.
//
// # Design trade-offs
//
// The design deliberately trades sub-second precision for operational
// simplicity: notifications fire within one interval of their scheduled time,
// never earlier. The atomic UpdateStatusIfScheduled guard makes the scheduler
// safe to run as a redundant set of replicas — only one instance will win the
// state transition and publish the Kafka event for a given notification.
//
// # Configuration and lifecycle
//
// Configuration is loaded from the environment via config.LoadSchedulerConfig
// (see SCHEDULER_INTERVAL). The process shuts down gracefully on
// SIGINT/SIGTERM.
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

// main runs the polling loop that promotes due scheduled notifications into
// the Kafka send queue.
//
// # Sequence
//
//  1. Load scheduler configuration and open PostgreSQL. Connection failure is
//     fatal — without the database the scheduler has nothing to poll.
//  2. Build the notification repository and the Kafka producer targeting the
//     send topic.
//  3. Parse SCHEDULER_INTERVAL into a time.Duration and start a ticker. An
//     invalid interval is fatal.
//  4. Invoke processScheduled once immediately so that restarts do not delay
//     already-due notifications by a full interval.
//  5. Run the ticker in a background goroutine and block on SIGINT/SIGTERM.
//
// # Concurrency
//
// The initial synchronous call and the ticker-driven calls are the only
// writers to the notification state machine from this process. The repository
// enforces a conditional update (SCHEDULED -> QUEUED), which keeps
// multi-replica deployments safe: at most one replica will observe a
// rowsAffected > 0 result for any given notification.
//
// main takes no parameters and never returns; termination is by log.Fatal on
// configuration or database errors, or by SIGINT/SIGTERM.
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

// processScheduled fetches due scheduled notifications and queues them to
// Kafka.
//
// It retrieves up to 100 notifications whose scheduled_at is in the past and
// whose status is still SCHEDULED, then for each one:
//
//  1. Atomically transitions the status from SCHEDULED to QUEUED using
//     UpdateStatusIfScheduled. If zero rows are affected, another replica (or
//     a concurrent tick) has already claimed the notification and it is
//     skipped.
//  2. Publishes a kafka.NotificationSendRequested event to the send topic.
//  3. On publish failure, attempts to roll the status back to SCHEDULED so
//     that a later tick can retry.
//
// # Error handling
//
// Errors are logged and do not abort the batch — one bad notification does
// not prevent the rest of the due set from being queued. A failure to fetch
// the due set itself aborts the call for this tick (the next tick will retry).
//
// # Parameters
//
// ctx is passed through to the repository and Kafka producer and should carry
// request-scoped deadlines when invoked from an HTTP context; the scheduler
// invokes it with the process-lifetime context, so the call is only cancelled
// when the process is shutting down.
//
// repo and producer must be non-nil; nil values will panic on first use.
//
// processScheduled is not safe for concurrent invocation from multiple
// goroutines within the same process — it relies on the atomic DB guard for
// cross-process safety, not on in-process locking.
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
