package auth

import (
	"encoding/json"
	"net/http"

	"hello/pkg/response"
)

type AuthHandler struct {
	service *AuthService
}

func NewAuthHandler(s *AuthService) *AuthHandler {
	return &AuthHandler{
		service: s,
	}
}

func (h *AuthHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid body")
		return
	}

	user, found := h.service.GetUser(req.Email, req.Password)

	if !found {
		response.JSON(w, http.StatusUnauthorized, nil, "invalid credentials")
		return
	}

	accessToken, err := GenerateAccessToken(*user)

	if err != nil {
		response.JSON(w, http.StatusInternalServerError, nil, "failed to access generate token")
		return
	}

	refreshToken, err := GenerateRefreshToken(*user)

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
