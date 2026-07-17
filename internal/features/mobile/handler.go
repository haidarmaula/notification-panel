package mobile

import (
	"encoding/json"
	"errors"
	"net/http"

	"hello/pkg/response"
)

// MobileHandler handles HTTP requests for mobile app integration.
type MobileHandler struct {
	service *MobileService
}

// NewMobileHandler creates a new MobileHandler instance.
func NewMobileHandler(service *MobileService) *MobileHandler {
	return &MobileHandler{service: service}
}

// Sync handles POST /api/v1/mobile/sync.
func (h *MobileHandler) Sync(w http.ResponseWriter, r *http.Request) {
	var req SyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	if req.JWT == "" {
		response.JSON(w, http.StatusBadRequest, nil, "jwt is required")
		return
	}
	if req.DeviceToken.PushToken == "" {
		response.JSON(w, http.StatusBadRequest, nil, "push_token is required")
		return
	}
	if req.DeviceToken.Platform == "" {
		response.JSON(w, http.StatusBadRequest, nil, "platform is required")
		return
	}

	result, err := h.service.Sync(r.Context(), SyncParams{
		JWT:            req.JWT,
		Platform:       req.DeviceToken.Platform,
		PushToken:      req.DeviceToken.PushToken,
		InstallationID: req.DeviceToken.InstallationID,
		AppVersion:     req.DeviceToken.AppVersion,
		OSVersion:      req.DeviceToken.OSVersion,
		DeviceModel:    req.DeviceToken.DeviceModel,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidJWT):
			response.JSON(w, http.StatusUnauthorized, nil, err.Error())
		case errors.Is(err, ErrInvalidPlatform):
			response.JSON(w, http.StatusBadRequest, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		}
		return
	}

	resp := SyncResponse{
		UserID:        result.UserID,
		DeviceTokenID: result.DeviceTokenID,
	}
	response.JSON(w, http.StatusOK, resp, "user synchronized")
}
