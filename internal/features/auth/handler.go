package auth

import (
	"errors"
	"net/http"

	"hello/internal/httprequest"
	"hello/internal/token"
	"hello/pkg/response"
)

type AuthHandler struct{ service *AuthService }

func NewAuthHandler(service *AuthService) *AuthHandler { return &AuthHandler{service: service} }

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if !httprequest.DecodeJSON(w, r, &req, httprequest.DefaultJSONLimit) {
		return
	}
	pair, err := h.service.Login(r.Context(), req.Email, req.Password)
	credentialResponse(w, pair, err)
}

func (h *AuthHandler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req RefreshTokenRequest
	if !httprequest.DecodeJSON(w, r, &req, httprequest.DefaultJSONLimit) {
		return
	}
	pair, err := h.service.RefreshToken(r.Context(), req.RefreshToken)
	credentialResponse(w, pair, err)
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	var req RefreshTokenRequest
	if !httprequest.DecodeJSON(w, r, &req, httprequest.DefaultJSONLimit) {
		return
	}
	credentialResponse(w, nil, h.service.Logout(r.Context(), req.RefreshToken))
}

func credentialResponse(w http.ResponseWriter, data *LoginResponse, err error) {
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case err == nil:
		response.JSON(w, http.StatusOK, data, "success")
	case errors.Is(err, ErrInvalidCredentials), errors.Is(err, token.ErrInvalidToken):
		response.JSON(w, http.StatusUnauthorized, nil, err.Error())
	default:
		response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
	}
}
