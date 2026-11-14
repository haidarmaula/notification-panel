package config

import (
	"log"
	"os"

	"hello/internal/ingress"
	"hello/internal/observability"
	"hello/internal/token"
)

// ServerConfig holds the configuration required by the HTTP API server
// (cmd/server) in addition to the shared database settings.
//
// It covers the HTTP listener, staff JWT secrets, the shared API key, browser
// CORS origins, and mobile installation policy used by current server routes.
type ServerConfig struct {
	Gateway            *ingress.Gateway
	Monitoring         *observability.Admin
	ListenAddress      string
	CORSAllowedOrigins []string
	mobilePolicyFile   string
	staffTokens        *token.TokenManager

	BaseConfig

	// APIKey is the shared secret expected in the X-API-Key header on every
	// request (API_KEY). It provides a first line of defence before JWT
	// validation and is shared with the admin web portal. Rotating it
	// requires coordinating with the portal deployment.
	APIKey string

	// AccessSecret is the local development HMAC secret used to sign short-lived access
	// tokens (ACCESS_SECRET). Must be kept private; leaking it allows an
	// attacker to mint valid access tokens.
	AccessSecret string

	// RefreshSecret is the local development HMAC secret used to sign refresh tokens
	// (REFRESH_SECRET). It is kept separate from AccessSecret so that the
	// blast radius of a leak is limited to one token class.
	RefreshSecret string
}

// StaffTokens returns the immutable protected issuer or the local environment issuer.
func (c *ServerConfig) StaffTokens() *token.TokenManager {
	if c.protectedDatabase != nil {
		return c.staffTokens
	}
	return token.NewTokenManager(c.AccessSecret, c.RefreshSecret)
}

// LoadServerConfig reads the API server configuration from the process
// environment, delegating the shared database fields to LoadBaseConfig.
//
// # Failure modes
//
// The function calls log.Fatal and terminates the process if API_KEY,
// ACCESS_SECRET, or REFRESH_SECRET is empty, if CORS_ALLOWED_ORIGINS is
// malformed, or if the underlying LoadBaseConfig fails. The listener defaults
// to loopback, and an empty CORS origin list denies cross-origin browser
// access. It never returns nil.
//
// # Concurrency
//
// Intended for one-shot use during process startup; not safe to call
// concurrently with other environment-mutating code.
func LoadServerConfig() *ServerConfig {
	base := LoadBaseConfig()

	cfg := &ServerConfig{
		BaseConfig:       *base,
		ListenAddress:    os.Getenv("HTTP_LISTEN_ADDRESS"),
		mobilePolicyFile: os.Getenv("MOBILE_POLICY_FILE"),

		APIKey:        os.Getenv("API_KEY"),
		AccessSecret:  os.Getenv("ACCESS_SECRET"),
		RefreshSecret: os.Getenv("REFRESH_SECRET"),
	}
	if cfg.ListenAddress == "" {
		cfg.ListenAddress = "127.0.0.1:8080"
	}
	var err error
	cfg.CORSAllowedOrigins, err = parseCORSOriginList(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if err != nil {
		log.Fatal("CORS_ALLOWED_ORIGINS is invalid")
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

	return cfg
}
