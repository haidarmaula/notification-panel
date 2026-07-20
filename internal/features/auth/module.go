package auth

import (
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

// AuthModule represents the authentication feature module.
type AuthModule struct {
	middlewares []middleware.Middleware
	handler     *AuthHandler
}

// NewAuthModule creates a new AuthModule instance with the required dependencies.
func NewAuthModule(queries *sqlc.Queries, tokenManager *token.TokenManager, middlewares ...middleware.Middleware) *AuthModule {
	repo := repository.NewStaffUserRepository(queries)
	service := NewAuthService(repo, tokenManager)
	handler := NewAuthHandler(service)

	return &AuthModule{
		middlewares: middlewares,
		handler:     handler,
	}
}
