package mobile

import (
	"hello/internal/config"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
)

// MobileModule represents the mobile integration module.
type MobileModule struct {
	middlewares []middleware.Middleware
	handler     *MobileHandler
}

// NewMobileModule creates a new MobileModule instance.
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
