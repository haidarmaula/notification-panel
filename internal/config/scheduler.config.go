package config

import (
	"log"
	"os"
)

type SchedulerConfig struct {
	BaseConfig

	SchedulerInterval string // interval between scheduler runs (e.g., "30s")

	KafkaBroker string
	SendTopic   string
}

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
