package users

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"hello/pkg/response"
)

// DeviceTokenHandler handles HTTP requests for device token management.
type DeviceTokenHandler struct {
	service *UserService
}

// NewDeviceTokenHandler creates a new DeviceTokenHandler instance.
func NewDeviceTokenHandler(service *UserService) *DeviceTokenHandler {
	return &DeviceTokenHandler{service: service}
}

// ListByUser handles GET /api/v1/users/{id}/device-tokens.
func (h *DeviceTokenHandler) ListByUser(w http.ResponseWriter, r *http.Request) {
	userID, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid user id")
		return
	}

	tokens, err := h.service.ListDeviceTokens(r.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	data := make([]DeviceTokenResponse, len(tokens))
	for i, t := range tokens {
		var lastSeen *string
		if t.LastSeenAt != nil {
			val := t.LastSeenAt.Format(time.RFC3339)
			lastSeen = &val
		}
		data[i] = DeviceTokenResponse{
			ID:             t.ID,
			Platform:       t.Platform,
			InstallationID: t.InstallationID,
			IsActive:       t.IsActive,
			LastSeenAt:     lastSeen,
			CreatedAt:      t.CreatedAt.Format(time.RFC3339),
		}
	}

	response.JSON(w, http.StatusOK, ListDeviceTokensResponse{Data: data}, "success")
}

// Register handles POST /api/v1/device-tokens (for mobile app).
func (h *DeviceTokenHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID int64 `json:"user_id"`
		RegisterDeviceTokenRequest
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}
	if req.UserID == 0 {
		response.JSON(w, http.StatusBadRequest, nil, "user_id is required")
		return
	}
	if req.PushToken == "" {
		response.JSON(w, http.StatusBadRequest, nil, "push_token is required")
		return
	}
	if req.Platform == "" {
		response.JSON(w, http.StatusBadRequest, nil, "platform is required")
		return
	}

	token, err := h.service.RegisterDeviceToken(r.Context(), req.UserID, req.RegisterDeviceTokenRequest)
	if err != nil {
		switch {
		case errors.Is(err, ErrUserNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrDeviceTokenDuplicate):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		case errors.Is(err, ErrInvalidPlatform):
			response.JSON(w, http.StatusBadRequest, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		}
		return
	}

	resp := DeviceTokenResponse{
		ID:             token.ID,
		Platform:       token.Platform,
		InstallationID: token.InstallationID,
		IsActive:       token.IsActive,
		CreatedAt:      token.CreatedAt.Format(time.RFC3339),
	}
	response.JSON(w, http.StatusCreated, resp, "device token registered")
}

// Update handles PATCH /api/v1/device-tokens/{id}.
func (h *DeviceTokenHandler) Update(w http.ResponseWriter, r *http.Request) {
	tokenID, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid token id")
		return
	}

	var req UpdateDeviceTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	if req.Platform == nil && req.PushToken == nil && req.InstallationID == nil &&
		req.AppVersion == nil && req.OSVersion == nil && req.DeviceModel == nil &&
		req.IsActive == nil {
		response.JSON(w, http.StatusBadRequest, nil, "at least one field required")
		return
	}

	err = h.service.UpdateDeviceToken(r.Context(), tokenID, req)
	if err != nil {
		switch {
		case errors.Is(err, ErrDeviceTokenNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrDeviceTokenDuplicate):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		case errors.Is(err, ErrInvalidPlatform):
			response.JSON(w, http.StatusBadRequest, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		}
		return
	}

	response.JSON(w, http.StatusOK, map[string]string{"message": "device token updated"}, "success")
}

// Delete handles DELETE /api/v1/device-tokens/{id}.
func (h *DeviceTokenHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tokenID, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid token id")
		return
	}

	err = h.service.DeleteDeviceToken(r.Context(), tokenID)
	if err != nil {
		if errors.Is(err, ErrDeviceTokenNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, nil, "deleted")
}
