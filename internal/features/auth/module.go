package auth

import (
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

type AuthModule struct {
	middlewares []middleware.Middleware
	handler     *AuthHandler
}

func NewAuthModule(queries *sqlc.Queries, tokenManager *token.TokenManager, middlewares ...middleware.Middleware) *AuthModule {
	repo := repository.NewStaffRepository(queries)
	service := NewAuthService(repo, tokenManager)
	handler := NewAuthHandler(service)

	return &AuthModule{
		middlewares: middlewares,
		handler:     handler,
	}
}
