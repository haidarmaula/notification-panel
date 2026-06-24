package auth

import (
	"net/http"
)

func RegisterModule(mux *http.ServeMux) {
	authRepo := NewAuthRepository()
	authService := NewAuthService(authRepo)
	authHandler := NewAuthHandler(authService)

	RegisterRoutes(mux, authHandler)
}
