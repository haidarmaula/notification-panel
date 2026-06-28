package auth

import (
	"hello/internal/middleware"
	"hello/internal/token"
)

type AuthModule struct {
	middlewares []middleware.Middleware
	handler     *AuthHandler
}

func NewAuthModule(tokenManager *token.TokenManager, middlewares ...middleware.Middleware) *AuthModule {
	repo := NewAuthRepository()
	service := NewAuthService(repo)
	handler := NewAuthHandler(service, tokenManager)

	return &AuthModule{
		middlewares: middlewares,
		handler:     handler,
	}
}
