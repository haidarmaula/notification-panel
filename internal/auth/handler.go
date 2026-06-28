package auth

import (
	"encoding/json"
	"net/http"

	"hello/internal/token"
	"hello/pkg/response"
)

type AuthHandler struct {
	service      *AuthService
	tokenManager *token.TokenManager
}

func NewAuthHandler(s *AuthService, tm *token.TokenManager) *AuthHandler {
	return &AuthHandler{
		service:      s,
		tokenManager: tm,
	}
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid body")
		return
	}

	user, found := h.service.Login(req.Email, req.Password)

	if !found {
		response.JSON(w, http.StatusUnauthorized, nil, "invalid credentials")
		return
	}

	accessToken, err := h.tokenManager.GenerateAccessToken(user.ID, user.Email)

	if err != nil {
		response.JSON(w, http.StatusInternalServerError, nil, "failed to access generate token")
		return
	}

	refreshToken, err := h.tokenManager.GenerateRefreshToken(user.ID)

	if err != nil {
		response.JSON(w, http.StatusInternalServerError, nil, "failed to refresh generate token")
		return
	}

	res := LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}

	response.JSON(w, http.StatusOK, res, "success")
}

func (h *AuthHandler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req RefreshTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid body")
		return
	}

	claims, err := h.tokenManager.ParseRefreshToken(req.RefreshToken)
	if err != nil {
		response.JSON(w, http.StatusUnauthorized, nil, "invalid token")
		return
	}

	user, found := h.service.GetUserByID(claims.UserID)
	if !found {
		response.JSON(w, http.StatusUnauthorized, nil, "user not found")
		return
	}

	accessToken, err := h.tokenManager.GenerateAccessToken(user.ID, user.Email)
	if err != nil {
		response.JSON(w, http.StatusInternalServerError, nil, "failed to generate access token")
		return
	}

	res := RefreshTokenResponse{
		AccessToken: accessToken,
	}

	response.JSON(w, http.StatusOK, res, "success")
}
