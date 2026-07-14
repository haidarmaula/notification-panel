package users

import (
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
)

// UserModule represents the user feature module.
type UserModule struct {
	middlewares        []middleware.Middleware
	handler            *UserHandler
	deviceTokenHandler *DeviceTokenHandler
}

// NewUserModule creates a new UserModule instance.
func NewUserModule(queries *sqlc.Queries, middlewares ...middleware.Middleware) *UserModule {
	userRepo := repository.NewUserRepository(queries)
	deviceRepo := repository.NewDeviceTokenRepository(queries)
	segmentRepo := repository.NewSegmentMemberRepository(queries)
	deliveryRepo := repository.NewNotificationDeliveryRepository(queries)
	readRepo := repository.NewNotificationReadRepository(queries)

	service := NewUserService(userRepo, deviceRepo, segmentRepo, deliveryRepo, readRepo)
	handler := NewUserHandler(service)
	deviceTokenHandler := NewDeviceTokenHandler(service)

	return &UserModule{
		middlewares:        middlewares,
		handler:            handler,
		deviceTokenHandler: deviceTokenHandler,
	}
}
