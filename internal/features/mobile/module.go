package mobile

import (
	"hello/internal/config"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
)

// MobileModule represents the mobile integration module.
//
// It is the composition root of the mobile feature: it owns the handler,
// holds the ordered chain of middlewares that should run in front of the
// mobile endpoints, and is the unit that cmd/server constructs once at
// startup. It follows the same shape as the auth module and every other
// feature module in this repository.
//
// # Why the module holds a config reference
//
// Unlike the auth module — which receives only a token manager and a set
// of repositories — the mobile module's constructor takes a whole
// *config.ServerConfig. This is because the mobile service needs the
// MobileJWTSecret, which is a distinct secret from the staff-facing
// AccessSecret and RefreshSecret. The module extracts only that one field
// from the config; it does not retain the config object after construction.
//
// # Concurrency
//
// The module is safe for concurrent use. Its fields are set once in the
// constructor and never mutated; RegisterRoutes only reads them.
type MobileModule struct {
	middlewares []middleware.Middleware
	handler     *MobileHandler
}

// NewMobileModule creates a new MobileModule instance.
//
// # Wiring
//
// The constructor performs the full dependency-injection chain for the
// mobile feature in a single call:
//
//  1. Builds a UserRepository and a DeviceTokenRepository from the
//     supplied sqlc.Queries. These are the only two repositories the
//     mobile feature needs: one for the user-upsert step, the other for
//     the device-token upsert.
//  2. Builds a MobileService from the two repositories and the mobile
//     JWT secret taken from cfg.MobileJWTSecret.
//  3. Builds a MobileHandler from the service.
//  4. Returns the module with the supplied middlewares and the handler.
//
// # Parameters
//
// queries must be non-nil and is expected to be the shared instance from
// cmd/server; the repositories it backs are safe for concurrent use and
// are shared with other feature modules. cfg must be non-nil and its
// MobileJWTSecret must be set — the config loader guarantees the latter,
// but the constructor does not re-validate it. If MobileJWTSecret is empty
// or mismatched with the value Laravel uses to sign, every sync attempt
// will fail with ErrInvalidJWT at the parse step.
//
// The middlewares parameter is variadic so that the caller can pass
// whichever cross-cutting concerns apply to the mobile endpoints. In the
// current wiring it consists of the API-key middleware only; the staff
// JWT middleware is deliberately excluded, because the mobile client
// authenticates with a Laravel-issued token carried in the request body
// rather than with a bearer token in the Authorization header.
//
// # Concurrency
//
// Intended to be called once during process startup, before the HTTP
// server begins listening.
func NewMobileModule(
	queries *sqlc.Queries,
	cfg *config.ServerConfig,
	middlewares ...middleware.Middleware,
) *MobileModule {
	userRepo := repository.NewUserRepository(queries)
	deviceRepo := repository.NewDeviceTokenRepository(queries)

	service := NewMobileService(userRepo, deviceRepo, cfg.MobileJWTSecret)
	handler := NewMobileHandler(service)

	return &MobileModule{
		middlewares: middlewares,
		handler:     handler,
	}
}
