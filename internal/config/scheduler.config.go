package config

import (
	"log"
	"os"
)

// SchedulerConfig holds the configuration required by the scheduled-
// notification processor (cmd/scheduler) in addition to the shared database
// settings.
//
// The scheduler's job is to poll the notifications table for due entries and
// publish them to Kafka, so it needs both the polling interval and the
// Kafka producer parameters. It does not need the OneSignal credentials,
// JWT secrets, or API keys used elsewhere — those are deliberately absent.
type SchedulerConfig struct {
	BaseConfig

	// SchedulerInterval is the polling interval for the scheduler's ticker,
	// expressed as a Go time.ParseDuration string (SCHEDULER_INTERVAL, e.g.
	// "30s", "1m", "500ms"). If the environment variable is unset, this
	// field defaults to "30s" rather than triggering a fatal error — the
	// only field in this package with a non-empty default. The string is
	// parsed by the scheduler at startup; an unparseable value is fatal
	// there, not here.
	SchedulerInterval string

	// KafkaBroker is the address of the Kafka broker used to publish send
	// requests (KAFKA_BROKER).
	KafkaBroker string

	// SendTopic is the Kafka topic to which the scheduler publishes
	// notification.send.requested events (KAFKA_SEND_TOPIC). It must match
	// the topic the worker consumes from.
	SendTopic string
}

// LoadSchedulerConfig reads the scheduler configuration from the process
// environment, delegating the shared database fields to LoadBaseConfig.
//
// # Defaults
//
// SchedulerInterval is the only field with a default: if SCHEDULER_INTERVAL
// is unset or empty, it is assigned "30s". All other required fields are
// validated and cause a fatal exit when missing.
//
// # Failure modes
//
// The function calls log.Fatal and terminates the process if KAFKA_BROKER or
// KAFKA_SEND_TOPIC is empty, or if the underlying LoadBaseConfig fails. It
// never returns nil and does not validate the syntax of SchedulerInterval —
// that check is performed by the caller when it parses the duration.
//
// # Concurrency
//
// Intended for one-shot use during process startup; not safe to call
// concurrently with other environment-mutating code.
func LoadSchedulerConfig() *SchedulerConfig {
	base := LoadBaseConfig()

	cfg := &SchedulerConfig{
		BaseConfig: *base,

		SchedulerInterval: os.Getenv("SCHEDULER_INTERVAL"),

		KafkaBroker: os.Getenv("KAFKA_BROKER"),
		SendTopic:   os.Getenv("KAFKA_SEND_TOPIC"),
	}

	if cfg.SchedulerInterval == "" {
		cfg.SchedulerInterval = "30s"
	}

	if cfg.KafkaBroker == "" {
		log.Fatal("KafkaBroker is required")
	}

	if cfg.SendTopic == "" {
		log.Fatal("SendTopic is required")
	}

	return cfg
}
