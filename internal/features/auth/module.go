package auth

import (
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

// AuthModule wires the staff credential exchange behind the server's API-key guard.
type AuthModule struct {
	middlewares []middleware.Middleware
	handler     *AuthHandler
}

func NewAuthModule(queries *sqlc.Queries, tokens *token.TokenManager, middlewares ...middleware.Middleware) *AuthModule {
	return &AuthModule{middlewares: middlewares, handler: NewAuthHandler(&AuthService{q: queries, tokens: tokens})}
}
