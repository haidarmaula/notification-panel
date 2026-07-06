package notifications

import (
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
)

// NotificationModule represents the notification feature module.
type NotificationModule struct {
	middlewares []middleware.Middleware
	handler     *NotificationHandler
}

// NewNotificationModule creates a new NotificationModule instance.
func NewNotificationModule(queries *sqlc.Queries, middlewares ...middleware.Middleware) *NotificationModule {
	notifRepo := repository.NewNotificationRepository(queries)
	targetRepo := repository.NewNotificationTargetRepository(queries)
	deliveryRepo := repository.NewNotificationDeliveryRepository(queries)
	readRepo := repository.NewNotificationReadRepository(queries)
	staffRepo := repository.NewStaffUserRepository(queries)
	templateRepo := repository.NewTemplateRepository(queries)
	segmentRepo := repository.NewSegmentRepository(queries)

	service := NewNotificationService(
		notifRepo, targetRepo, deliveryRepo, readRepo,
		staffRepo, templateRepo, segmentRepo,
	)
	handler := NewNotificationHandler(service)

	return &NotificationModule{
		middlewares: middlewares,
		handler:     handler,
	}
}
