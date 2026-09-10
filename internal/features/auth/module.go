package auth

import (
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

// AuthModule represents the authentication feature module.
//
// It is the composition root of the auth feature: it owns the handler, holds
// the ordered chain of middlewares that should run in front of the auth
// endpoints, and is the unit that cmd/server constructs once at startup.
// Nothing above this type knows how the service or its repositories were
// built, and nothing below it knows how the endpoints are exposed.
//
// # Design
//
// The module follows the same shape as every other feature module in this
// repository (users, staff, notifications, segments, profile, mobile). It is
// constructed with NewAuthModule, which performs the dependency-injection
// wiring, and it is attached to the server's ServeMux by RegisterRoutes.
// Middlewares are passed in by the caller rather than constructed here,
// because they are shared across features and must be the same instances —
// notably the JWT middleware, which holds the shared token manager.
//
// # Concurrency
//
// The module is safe for concurrent use. Its fields are set once in the
// constructor and never mutated; RegisterRoutes only reads them.
type AuthModule struct {
	middlewares []middleware.Middleware
	handler     *AuthHandler
}

// NewAuthModule creates a new AuthModule instance with the required dependencies.
//
// # Wiring
//
// The constructor performs the full dependency-injection chain for the auth
// feature in a single call:
//
//  1. Builds a StaffUserRepository and a RoleRepository from the supplied
//     sqlc.Queries. These are the only repositories the auth feature needs:
//     one to find a staff user by email or ID, the other to resolve the
//     role name that goes into the access token.
//  2. Builds an AuthService from the two repositories and the supplied
//     token manager.
//  3. Builds an AuthHandler from the service.
//  4. Returns the module with the supplied middlewares and the handler.
//
// # Parameters
//
// queries must be non-nil and is expected to be the shared instance from
// cmd/server; the repositories it backs are safe for concurrent use and are
// shared with other feature modules. tokenManager must be non-nil; it is
// the same manager used by the JWT middleware, so a mismatch between the
// manager passed here and the one used for verification would cause every
// token the module mints to be rejected on subsequent requests.
//
// The middlewares parameter is variadic so that the caller can pass however
// many cross-cutting concerns apply to the auth endpoints — the API-key
// middleware and nothing else, in the current configuration — without the
// module hardcoding a chain. The order in which they are supplied is the
// order in which they will be applied (see middleware.Chain for the
// composition rule).
//
// # Deliberate omissions
//
// The auth endpoints are not gated by the JWT middleware, and this
// constructor does not enforce that. The caller in cmd/server is
// responsible for passing only the API-key middleware here — a JWT gate on
// the login endpoint would be circular, since the client has not yet
// authenticated. If a future revision adds endpoints to this module that
// do require authentication (a logout endpoint, for example), the module
// would need to be split or the caller would need to pass a chain that
// conditionally applies.
//
// # Concurrency
//
// The constructor is not safe to call concurrently with itself on the same
// sqlc.Queries, because the two repository constructors are cheap and
// stateless — the risk is not corruption but pointless duplication. It is
// intended to be called once at process startup.
func NewAuthModule(queries *sqlc.Queries, tokenManager *token.TokenManager, middlewares ...middleware.Middleware) *AuthModule {
	staffRepo := repository.NewStaffUserRepository(queries)
	roleRepo := repository.NewRoleRepository(queries)
	service := NewAuthService(staffRepo, roleRepo, tokenManager)
	handler := NewAuthHandler(service)

	return &AuthModule{
		middlewares: middlewares,
		handler:     handler,
	}
}
