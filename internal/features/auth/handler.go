package auth

import (
	"encoding/json"
	"errors"
	"net/http"

	"hello/internal/token"
	"hello/pkg/response"
)

// AuthHandler handles HTTP requests for authentication endpoints.
type AuthHandler struct {
	service *AuthService
}

// NewAuthHandler creates a new AuthHandler instance.
func NewAuthHandler(service *AuthService) *AuthHandler {
	return &AuthHandler{
		service: service,
	}
}

// Login handles POST /api/v1/auth/login.
// It authenticates staff credentials and returns access/refresh tokens.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	res, err := h.service.Login(
		r.Context(),
		req.Email,
		req.Password,
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidCredentials):
			response.JSON(w, http.StatusUnauthorized, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		}
		return
	}

	response.JSON(w, http.StatusOK, LoginResponse{
		AccessToken:  res.AccessToken,
		RefreshToken: res.RefreshToken,
	}, "success")
}

// RefreshToken handles POST /api/v1/auth/refresh.
// It accepts a valid refresh token and returns a new access token.
func (h *AuthHandler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req RefreshTokenRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	res, err := h.service.RefreshToken(
		r.Context(),
		req.RefreshToken,
	)
	if err != nil {
		switch {
		case errors.Is(err, token.ErrInvalidToken):
			response.JSON(w, http.StatusUnauthorized, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		}
		return
	}

	response.JSON(w, http.StatusOK, res, "success")
}
