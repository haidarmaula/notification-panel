package config

import (
	"log"
	"os"
)

// ServerConfig holds the configuration required by the HTTP API server
// (cmd/server) in addition to the shared database settings.
//
// It covers three concerns: authentication secrets used to sign and verify
// JWTs, the shared API key used by the admin portal for coarse-grained
// authorisation, and the Kafka producer parameters used when a staff member
// triggers an immediate notification send. Delivery-provider credentials
// (OneSignal) are deliberately absent — only the worker talks to OneSignal.
type ServerConfig struct {
	BaseConfig

	// APIKey is the shared secret expected in the X-API-Key header on every
	// request (API_KEY). It provides a first line of defence before JWT
	// validation and is shared with the admin web portal. Rotating it
	// requires coordinating with the portal deployment.
	APIKey string

	// AccessSecret is the HMAC secret used to sign short-lived access
	// tokens (ACCESS_SECRET). Must be kept private; leaking it allows an
	// attacker to mint valid access tokens.
	AccessSecret string

	// RefreshSecret is the HMAC secret used to sign refresh tokens
	// (REFRESH_SECRET). It is kept separate from AccessSecret so that the
	// blast radius of a leak is limited to one token class.
	RefreshSecret string

	// MobileJWTSecret is the shared HMAC secret used to verify tokens issued
	// by the Laravel mobile backend (MOBILE_JWT_SECRET). The API server does
	// not mint these tokens; it only verifies them. It must match the
	// corresponding value in the Laravel deployment.
	MobileJWTSecret string

	// SuperAdminRole is the role name that grants unrestricted administrative
	// access (SUPER_ADMIN_ROLE). It is compared against the role attached to
	// the authenticated staff user by the SuperAdminMiddleware. Note that the
	// bootstrap utility does not read this field — see the bootstrap package
	// for the implications.
	SuperAdminRole string

	// KafkaBroker is the address of the Kafka broker used to publish send
	// requests when a staff member triggers an immediate notification send
	// (KAFKA_BROKER).
	KafkaBroker string

	// SendTopic is the Kafka topic to which immediate-send events are
	// published (KAFKA_SEND_TOPIC). It must match the topic the worker
	// consumes from.
	SendTopic string
}

// LoadServerConfig reads the API server configuration from the process
// environment, delegating the shared database fields to LoadBaseConfig.
//
// # Failure modes
//
// The function calls log.Fatal and terminates the process if any of API_KEY,
// ACCESS_SECRET, REFRESH_SECRET, MOBILE_JWT_SECRET, SUPER_ADMIN_ROLE,
// KAFKA_BROKER, or KAFKA_SEND_TOPIC is empty, or if the underlying
// LoadBaseConfig fails. There are no defaults: the API server refuses to
// start with an incomplete security configuration rather than risk running
// with a weak or missing secret. It never returns nil.
//
// # Concurrency
//
// Intended for one-shot use during process startup; not safe to call
// concurrently with other environment-mutating code.
func LoadServerConfig() *ServerConfig {
	base := LoadBaseConfig()

	cfg := &ServerConfig{
		BaseConfig: *base,

		APIKey:          os.Getenv("API_KEY"),
		AccessSecret:    os.Getenv("ACCESS_SECRET"),
		RefreshSecret:   os.Getenv("REFRESH_SECRET"),
		MobileJWTSecret: os.Getenv("MOBILE_JWT_SECRET"),
		SuperAdminRole:  os.Getenv("SUPER_ADMIN_ROLE"),

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
