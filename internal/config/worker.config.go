package config

import (
	"log"
	"os"
)

type WorkerConfig struct {
	BaseConfig

	OneSignalAppID  string
	OneSignalAPIKey string

	KafkaBroker string
	SendTopic   string
	UpdateTopic string
}

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
