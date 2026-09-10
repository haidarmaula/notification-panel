package config

import (
	"log"
	"os"
)

// BootstrapConfig holds the configuration required by the one-shot bootstrap
// utility (cmd/bootstrap) in addition to the shared database settings.
//
// It extends BaseConfig with the credentials for the initial super-admin
// staff account that the bootstrap utility provisions. None of the
// BootstrapAdmin* fields is used by any other executable; the API server,
// worker, and scheduler are unaware of them, which keeps the bootstrap
// credentials out of long-running processes.
type BootstrapConfig struct {
	BaseConfig

	// BootstrapAdminName is the display name of the initial super-admin
	// account (BOOTSTRAP_SUPER_ADMIN_NAME).
	BootstrapAdminName string

	// BootstrapAdminEmail is the login email of the initial super-admin
	// account (BOOTSTRAP_SUPER_ADMIN_EMAIL). It doubles as the idempotency
	// key for the bootstrap routine: if a staff user with this email already
	// exists, no account is created.
	BootstrapAdminEmail string

	// BootstrapAdminPassword is the plaintext password for the initial
	// super-admin account (BOOTSTRAP_SUPER_ADMIN_PASSWORD). It is bcrypt-hashed
	// before being persisted and is never logged. Because it is supplied
	// through the environment, it is visible to any process able to read the
	// environment — treat it as a bootstrap-only secret and rotate it after
	// first login.
	BootstrapAdminPassword string
}

// LoadBootstrapConfig reads the bootstrap configuration from the process
// environment, delegating the shared database fields to LoadBaseConfig.
//
// # Failure modes
//
// The function calls log.Fatal and terminates the process if any of
// BOOTSTRAP_SUPER_ADMIN_NAME, BOOTSTRAP_SUPER_ADMIN_EMAIL, or
// BOOTSTRAP_SUPER_ADMIN_PASSWORD is empty, or if the underlying
// LoadBaseConfig fails. It never returns nil.
//
// # Concurrency
//
// Like the other Load* constructors, this function is intended to be invoked
// once during process startup and is not safe to call concurrently with
// environment-mutating code.
func LoadBootstrapConfig() *BootstrapConfig {
	base := LoadBaseConfig()

	cfg := &BootstrapConfig{
		BaseConfig: *base,

		BootstrapAdminName:     os.Getenv("BOOTSTRAP_SUPER_ADMIN_NAME"),
		BootstrapAdminEmail:    os.Getenv("BOOTSTRAP_SUPER_ADMIN_EMAIL"),
		BootstrapAdminPassword: os.Getenv("BOOTSTRAP_SUPER_ADMIN_PASSWORD"),
	}

	if cfg.BootstrapAdminName == "" {
		log.Fatal("BootstrapAdminName is required")
	}

	if cfg.BootstrapAdminEmail == "" {
		log.Fatal("BootstrapAdminEmail is required")
	}

	if cfg.BootstrapAdminPassword == "" {
		log.Fatal("BootstrapAdminPassword is required")
	}

	return cfg
}
