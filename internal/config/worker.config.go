package config

import (
	"log"
	"os"
)

// WorkerConfig holds the configuration required by the Kafka delivery worker
// (cmd/worker) in addition to the shared database settings.
//
// The worker is the only executable in the system that talks to the push
// provider, so this struct is the only place the OneSignal credentials
// appear. It also needs both Kafka topics — the send topic it consumes from
// and the update topic it publishes delivery outcomes to.
type WorkerConfig struct {
	BaseConfig

	// OneSignalAppID is the OneSignal application identifier
	// (ONESIGNAL_APP_ID) used as a path component in every OneSignal REST
	// call.
	OneSignalAppID string

	// OneSignalAPIKey is the OneSignal REST API key
	// (ONESIGNAL_API_KEY) sent in the Authorization header. Treat it as a
	// secret; leaking it allows an attacker to send notifications to this
	// application's users.
	OneSignalAPIKey string

	// KafkaBroker is the address of the Kafka broker from which the worker
	// consumes send requests and to which it publishes delivery updates
	// (KAFKA_BROKER).
	KafkaBroker string

	// SendTopic is the Kafka topic the worker consumes
	// notification.send.requested events from (KAFKA_SEND_TOPIC). It must
	// match the topic the scheduler and API server publish to.
	SendTopic string

	// UpdateTopic is the Kafka topic the worker publishes
	// notification.delivery.updated events to (KAFKA_UPDATE_TOPIC), for
	// downstream analytics consumers.
	UpdateTopic string
}

// LoadWorkerConfig reads the delivery worker configuration from the process
// environment, delegating the shared database fields to LoadBaseConfig.
//
// # Failure modes
//
// The function calls log.Fatal and terminates the process if any of
// ONESIGNAL_APP_ID, ONESIGNAL_API_KEY, KAFKA_BROKER, KAFKA_SEND_TOPIC, or
// KAFKA_UPDATE_TOPIC is empty, or if the underlying LoadBaseConfig fails.
// There are no defaults. It never returns nil.
//
// # Concurrency
//
// Intended for one-shot use during process startup; not safe to call
// concurrently with other environment-mutating code.
func LoadWorkerConfig() *WorkerConfig {
	base := LoadBaseConfig()

	cfg := &WorkerConfig{
		BaseConfig: *base,

		OneSignalAppID:  os.Getenv("ONESIGNAL_APP_ID"),
		OneSignalAPIKey: os.Getenv("ONESIGNAL_API_KEY"),

		KafkaBroker: os.Getenv("KAFKA_BROKER"),
		SendTopic:   os.Getenv("KAFKA_SEND_TOPIC"),
		UpdateTopic: os.Getenv("KAFKA_UPDATE_TOPIC"),
	}

	if cfg.OneSignalAppID == "" {
		log.Fatal("ONESIGNAL_APP_ID is required")
	}

	if cfg.OneSignalAPIKey == "" {
		log.Fatal("ONESIGNAL_API_KEY is required")
	}

	if cfg.KafkaBroker == "" {
		log.Fatal("KafkaBroker is required")
	}

	if cfg.SendTopic == "" {
		log.Fatal("SendTopic is required")
	}

	if cfg.UpdateTopic == "" {
		log.Fatal("UpdateTopic is required")
	}

	return cfg
}
