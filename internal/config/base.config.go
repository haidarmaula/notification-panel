// Package config centralises environment-driven configuration for every
// executable in the Notification Panel: the HTTP API server, the Kafka
// delivery worker, the scheduled-notification processor, and the one-shot
// bootstrap utility.
//
// # Loading model
//
// Each executable loads exactly one specialised config struct — ServerConfig,
// WorkerConfig, SchedulerConfig, or BootstrapConfig — each of which embeds
// the shared BaseConfig and adds the environment variables that matter to
// that binary. There is no single global config: a running API server has no
// knowledge of the worker's OneSignal credentials, and vice versa, which
// keeps each binary's required environment minimal.
//
// # Fail-fast policy
//
// The Load* constructors do not return an error. They validate every required
// environment variable and terminate the process with log.Fatal if any is
// missing. This is deliberate: a misconfigured process cannot serve traffic
// meaningfully, and surfacing the problem at startup is preferable to a
// partial rollout followed by a runtime panic. Callers therefore never need
// to check for a nil config — the function either returns a fully-populated
// struct or does not return at all.
//
// # .env file handling
//
// LoadBaseConfig calls godotenv.Load, which reads a .env file in the current
// working directory and injects its values into the process environment
// without overwriting variables that are already set. Every specialised
// loader invokes LoadBaseConfig as its first step, so .env is loaded exactly
// once per process, at the entry point. In production this is typically a
// no-op — the deployment supplies environment variables directly — but it
// makes local development and `docker-compose up` work without ceremony.
//
// # Concurrency
//
// Config values are read once at startup and treated as immutable thereafter.
// No synchronisation is performed on the structs, because none is needed:
// after Load* returns, the config is read-only and safe to share across
// goroutines. Callers must not mutate config fields after handing the struct
// to other components.
package config

import (
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
)

// BaseConfig holds the PostgreSQL connection parameters shared by every
// executable in the system.
//
// It is embedded by value into the specialised config structs (ServerConfig,
// WorkerConfig, SchedulerConfig, BootstrapConfig), which is why it is
// exported but rarely used directly. Construct one via LoadBaseConfig rather
// than assembling it by hand so that the environment-variable contract is
// enforced consistently.
//
// All fields are populated from environment variables and are validated as
// non-empty at load time; none of them carries a default.
type BaseConfig struct {
	// DBHost is the PostgreSQL host (DB_HOST). May be a hostname, IP, or
	// Docker service name.
	DBHost string

	// DBPort is the PostgreSQL port as a string (DB_PORT). It is kept as a
	// string rather than an int so that it can be interpolated directly into
	// the DSN returned by GetDatabaseURL; the value is not validated for
	// numeric format.
	DBPort string

	// DBUser is the PostgreSQL user used for the application connection
	// (DB_USER).
	DBUser string

	// DBPassword is the PostgreSQL password (DB_PASSWORD). Treated as opaque
	// and never logged. Note that GetDatabaseURL does not URL-escape this
	// value — see the method documentation for the consequences.
	DBPassword string

	// DBName is the target database name (DB_NAME).
	DBName string

	// DBSSLMode is the sslmode parameter passed to PostgreSQL (DB_SSLMODE).
	// Typical values are "disable" for local development and "require" or
	// "verify-full" in production.
	DBSSLMode string
}

// LoadBaseConfig reads the shared database configuration from the process
// environment, loading a .env file from the current working directory first
// if one is present.
//
// # Side effects
//
// The call to godotenv.Load injects the contents of a .env file into the
// process environment. Existing variables take precedence, so a value set in
// the shell or by the container runtime is never overridden by the file.
// Because every specialised loader delegates to this function, .env is
// loaded exactly once per process.
//
// # Failure modes
//
// If any of DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME, or DB_SSLMODE is
// empty after .env loading, the function calls log.Fatal and the process
// exits with a non-zero status. It therefore never returns a partially
// populated config and never returns nil.
//
// # Concurrency
//
// LoadBaseConfig is intended to be called once during process startup, before
// any goroutines are spawned. It is not safe to call concurrently with other
// environment-mutating code because godotenv.Load mutates the process
// environment.
func LoadBaseConfig() *BaseConfig {
	godotenv.Load()

	cfg := &BaseConfig{
		DBHost:     os.Getenv("DB_HOST"),
		DBPort:     os.Getenv("DB_PORT"),
		DBUser:     os.Getenv("DB_USER"),
		DBPassword: os.Getenv("DB_PASSWORD"),
		DBName:     os.Getenv("DB_NAME"),
		DBSSLMode:  os.Getenv("DB_SSLMODE"),
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

	return cfg
}

// GetDatabaseURL assembles a PostgreSQL connection URL from the receiver's
// fields, in the form expected by pgx and lib/pq:
//
//	postgres://<user>:<password>@<host>:<port>/<name>?sslmode=<sslmode>
//
// # Escaping
//
// The method uses fmt.Sprintf rather than net/url to build the DSN, which
// means no percent-encoding is applied to any component. A password
// containing characters with special meaning in a URL — most commonly "@",
// ":", "/", "?", or "#" — will produce a malformed DSN and the driver will
// fail to parse it. Passwords for this application are expected to be
// alphanumeric; if that assumption ever changes, this method must be
// revisited before the change is deployed.
//
// # Failure modes
//
// GetDatabaseURL never returns an error and never panics: it will happily
// format a URL from any combination of field values, including empty ones.
// Validation of the underlying fields is the responsibility of LoadBaseConfig,
// not of this accessor.
//
// # Concurrency
//
// The method reads the receiver's fields and performs no mutation, so it is
// safe to call from multiple goroutines provided the receiver is not being
// mutated concurrently — which, per the package-level contract, it never is.
func (c *BaseConfig) GetDatabaseURL() string {
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
