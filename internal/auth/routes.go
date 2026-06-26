package auth

import (
	"net/http"
)

func RegisterRoutes(mux *http.ServeMux, handler *AuthHandler) {
	const prefix = "/api/v1/auth"

	mux.HandleFunc("POST "+prefix+"/login", handler.Login)
	mux.HandleFunc("POST "+prefix+"/refresh", handler.RefreshToken)
}
