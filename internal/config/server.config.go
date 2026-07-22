package config

import (
	"log"
	"os"
)

type ServerConfig struct {
	BaseConfig

	APIKey          string
	AccessSecret    string
	RefreshSecret   string
	MobileJWTSecret string // JWT shared secret with Laravel backend
	SuperAdminRole  string

	BootstrapAdminName     string
	BootstrapAdminEmail    string
	BootstrapAdminPassword string

	KafkaBroker string
	SendTopic   string
}

func LoadServerConfig() *ServerConfig {
	base := LoadBaseConfig()

	cfg := &ServerConfig{
		BaseConfig: *base,

		APIKey:          os.Getenv("API_KEY"),
		AccessSecret:    os.Getenv("ACCESS_SECRET"),
		RefreshSecret:   os.Getenv("REFRESH_SECRET"),
		MobileJWTSecret: os.Getenv("MOBILE_JWT_SECRET"),
		SuperAdminRole:  os.Getenv("SUPER_ADMIN_ROLE"),

		BootstrapAdminName:     os.Getenv("BOOTSTRAP_SUPER_ADMIN_NAME"),
		BootstrapAdminEmail:    os.Getenv("BOOTSTRAP_SUPER_ADMIN_EMAIL"),
		BootstrapAdminPassword: os.Getenv("BOOTSTRAP_SUPER_ADMIN_PASSWORD"),

		KafkaBroker: os.Getenv("KAFKA_BROKER"),
		SendTopic:   os.Getenv("KAFKA_SEND_TOPIC"),
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

	if cfg.SuperAdminRole == "" {
		log.Fatal("SUPER_ADMIN_ROLE is required")
	}

	if cfg.KafkaBroker == "" {
		log.Fatal("KafkaBroker is required")
	}

	if cfg.SendTopic == "" {
		log.Fatal("SendTopic is required")
	}

	return cfg
}
