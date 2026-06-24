package auth

import (
	"net/http"
)

func RegisterRoutes(mux *http.ServeMux, handler *AuthHandler) {
	const prefix = "/api/v1/auth"

	mux.HandleFunc("POST "+prefix, handler.GetUser)
}
