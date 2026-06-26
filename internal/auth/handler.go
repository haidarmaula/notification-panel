package auth

import (
	"encoding/json"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
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

func (h *AuthHandler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req RefreshTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid body")
		return
	}

	token, err := jwt.ParseWithClaims(
		req.RefreshToken,
		&RefreshClaims{},
		func(t *jwt.Token) (any, error) {
			return refreshSecret, nil
		},
	)
	if err != nil || !token.Valid {
		response.JSON(w, http.StatusUnauthorized, nil, "invalid refresh token")
		return
	}

	claims := token.Claims.(*RefreshClaims)
	if claims.Type != "refresh" {
		response.JSON(w, http.StatusUnauthorized, nil, "invalid token type")
		return
	}

	user, found := h.service.GetUserByID(claims.UserID)
	if !found {
		response.JSON(w, http.StatusUnauthorized, nil, "user not found")
		return
	}

	accessToken, err := GenerateAccessToken(*user)
	if err != nil {
		response.JSON(w, http.StatusInternalServerError, nil, "failed to generate access token")
		return
	}

	res := RefreshTokenResponse{
		AccessToken: accessToken,
	}

	response.JSON(w, http.StatusOK, res, "success")
}
