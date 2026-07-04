package notifications

import (
	"hello/internal/middleware"
)

type NotificationModule struct {
	middlewares []middleware.Middleware
	handler     *NotificationHandler
}

func NewNotificationModule(middlewares ...middleware.Middleware) *NotificationModule {
	repo := NewNotificationRepository()
	service := NewNotificationService(repo)
	handler := NewNotificationHandler(service)

	return &NotificationModule{
		middlewares: middlewares,
		handler:     handler,
	}
}
