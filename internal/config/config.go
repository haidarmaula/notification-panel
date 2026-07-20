package config

import (
	"encoding/base64"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	APIKey string

	AccessSecret  string
	RefreshSecret string

	MobileJWTSecret string // JWT shared secret with Laravel backend
	FCMCredentials  string // base64 encoded service account JSON
	OneSignalAppID  string
	OneSignalAPIKey string

	SchedulerInterval string // interval between scheduler runs (e.g., "30s")

	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string

	BootstrapAdminName     string
	BootstrapAdminEmail    string
	BootstrapAdminPassword string

	KafkaBroker string
	SendTopic   string
	UpdateTopic string
}

func Load() *Config {
	godotenv.Load()

	cfg := &Config{
		APIKey: os.Getenv("API_KEY"),

		AccessSecret:    os.Getenv("ACCESS_SECRET"),
		RefreshSecret:   os.Getenv("REFRESH_SECRET"),
		MobileJWTSecret: os.Getenv("MOBILE_JWT_SECRET"),
		FCMCredentials:  os.Getenv("FCM_CREDENTIALS"),
		OneSignalAppID:  os.Getenv("ONESIGNAL_APP_ID"),
		OneSignalAPIKey: os.Getenv("ONESIGNAL_API_KEY"),

		SchedulerInterval: os.Getenv("SCHEDULER_INTERVAL"),

		DBHost:     os.Getenv("DB_HOST"),
		DBPort:     os.Getenv("DB_PORT"),
		DBUser:     os.Getenv("DB_USER"),
		DBPassword: os.Getenv("DB_PASSWORD"),
		DBName:     os.Getenv("DB_NAME"),
		DBSSLMode:  os.Getenv("DB_SSLMODE"),

		BootstrapAdminName:     os.Getenv("BOOTSTRAP_SUPER_ADMIN_NAME"),
		BootstrapAdminEmail:    os.Getenv("BOOTSTRAP_SUPER_ADMIN_EMAIL"),
		BootstrapAdminPassword: os.Getenv("BOOTSTRAP_SUPER_ADMIN_PASSWORD"),

		KafkaBroker: os.Getenv("KAFKA_BROKER"),
		SendTopic:   os.Getenv("KAFKA_SEND_TOPIC"),
		UpdateTopic: os.Getenv("KAFKA_UPDATE_TOPIC"),
	}

	if cfg.APIKey == "" {
		log.Fatal("API_KEY is required")
	}

	if cfg.AccessSecret == "" {
		log.Fatal("ACCESS_SECRET is required")
	}

	if cfg.RefreshSecret == "" {
		log.Fatal("REFRESH_SECRET is required")
	}

	if cfg.MobileJWTSecret == "" {
		log.Fatal("MOBILE_JWT_SECRET is required")
	}

	// if cfg.FCMCredentials == "" {
	// 	log.Fatal("FCM_CREDENTIALS is required")
	// }

	if cfg.OneSignalAppID == "" {
		log.Fatal("ONESIGNAL_APP_ID is required")
	}

	if cfg.OneSignalAPIKey == "" {
		log.Fatal("ONESIGNAL_API_KEY is required")
	}

	if cfg.SchedulerInterval == "" {
		cfg.SchedulerInterval = "30s"
	}

	if cfg.DBHost == "" {
		log.Fatal("DB_HOST is required")
	}

	if cfg.DBPort == "" {
		log.Fatal("DB_PORT is required")
	}

	if cfg.DBUser == "" {
		log.Fatal("DB_USER is required")
	}

	if cfg.DBPassword == "" {
		log.Fatal("DB_PASSWORD is required")
	}

	if cfg.DBName == "" {
		log.Fatal("DB_NAME is required")
	}

	if cfg.DBSSLMode == "" {
		log.Fatal("DB_SSLMODE is required")
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

func (c *Config) GetDatabaseURL() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=%s",
		c.DBUser,
		c.DBPassword,
		c.DBHost,
		c.DBPort,
		c.DBName,
		c.DBSSLMode,
	)
}

// GetFCMCredentialsBytes returns decoded FCM credentials.
func (c *Config) GetFCMCredentialsBytes() ([]byte, error) {
	return base64.StdEncoding.DecodeString(c.FCMCredentials)
}
