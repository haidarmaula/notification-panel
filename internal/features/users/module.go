package users

import (
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
)

// UserModule represents the user feature module.
//
// It is the composition root of the users feature: it owns two HTTP
// handlers (UserHandler and DeviceTokenHandler) that share a single
// UserService, holds the ordered chain of middlewares that gate the
// feature's endpoints, and is the unit that cmd/server constructs once at
// startup. It follows the same shape as the auth, mobile, notifications,
// profile, segments, and staff modules, with one structural property
// worth noting — the two handlers are independent but co-located in the
// same module because they share a service and operate on overlapping
// data.
//
// # What the module owns
//
// On construction, the module builds and retains:
//
//   - Five repositories (user, device-token, segment-member, delivery,
//     notification-read) built from the shared sqlc.Queries.
//   - A UserService composed from all five.
//   - A UserHandler and a DeviceTokenHandler, both wrapping the same
//     service.
//
// The module does not own the dependencies it receives — the sqlc.Queries
// and the middlewares belong to the caller — and it does not expose any of
// its internal repositories or the service to the caller.
//
// # No audit service
//
// Unlike the staff, notifications, and segments modules, this module does
// not wire an audit service. The users feature performs no administrative
// write operations against the users table — it is read-only — so there
// is nothing to record. The device-token surface does write, but those
// writes are user-initiated device registrations rather than
// administrative actions, and they are not currently audited. If
// device-token writes become administratively significant (for example,
// if a super-admin needs to revoke a specific device), an audit
// dependency would need to be threaded through the module and the
// service.
//
// # Concurrency
//
// The module is safe for concurrent use. Its fields are set once in the
// constructor and never mutated; RegisterRoutes only reads them.
type UserModule struct {
	adminMiddlewares   []middleware.Middleware
	deviceMiddlewares  []middleware.Middleware
	handler            *UserHandler
	deviceTokenHandler *DeviceTokenHandler
}

// NewUserModule creates a new UserModule instance.
//
// # Wiring
//
// The constructor performs the full dependency-injection chain for the
// users feature in a single call:
//
//  1. Builds five repositories from the supplied sqlc.Queries:
//     - userRepo, backing all users-table access.
//     - deviceRepo, backing device-token CRUD.
//     - segmentRepo, backing the user-segments projection.
//     - deliveryRepo, backing the notification-history projection.
//     - readRepo, which is stored on the service but not referenced by
//     any method — see NewUserService for the dead-field note.
//  2. Builds a UserService from the five collaborators.
//  3. Builds a UserHandler and a DeviceTokenHandler, both wrapping the
//     same service.
//  4. Returns the module with the supplied middlewares and both handlers.
//
// # Parameters
//
// queries must be non-nil and is expected to be the shared instance from
// cmd/server. adminMiddlewares protect the staff-facing /users routes; in
// current wiring they require the API key and a staff JWT. deviceMiddlewares
// protect the mobile-facing /device-tokens routes; in current wiring they
// require a Laravel mobile JWT. Each slice is applied in order by
// middleware.Chain.
//
// # Concurrency
//
// Intended to be called once during process startup, before the HTTP
// server begins listening.
func NewUserModule(
	queries *sqlc.Queries,
	adminMiddlewares []middleware.Middleware,
	deviceMiddlewares []middleware.Middleware,
) *UserModule {
	userRepo := repository.NewUserRepository(queries)
	deviceRepo := repository.NewDeviceTokenRepository(queries)
	segmentRepo := repository.NewSegmentMemberRepository(queries)
	deliveryRepo := repository.NewNotificationDeliveryRepository(queries)
	readRepo := repository.NewNotificationReadRepository(queries)

	service := NewUserService(userRepo, deviceRepo, segmentRepo, deliveryRepo, readRepo)
	handler := NewUserHandler(service)
	deviceTokenHandler := NewDeviceTokenHandler(service)

	return &UserModule{
		adminMiddlewares:   adminMiddlewares,
		deviceMiddlewares:  deviceMiddlewares,
		handler:            handler,
		deviceTokenHandler: deviceTokenHandler,
	}
}
