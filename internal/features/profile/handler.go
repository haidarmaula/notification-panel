package profile

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"hello/pkg/response"
)

// ProfileHandler handles HTTP requests for staff profile management.
type ProfileHandler struct {
	service *ProfileService
}

// NewProfileHandler creates a new ProfileHandler instance.
func NewProfileHandler(service *ProfileService) *ProfileHandler {
	return &ProfileHandler{service: service}
}

// GetProfile handles GET /api/v1/profile.
// It returns the profile of the authenticated staff user.
func (h *ProfileHandler) GetProfile(w http.ResponseWriter, r *http.Request) {
	staffID, ok := GetStaffIDFromContext(r.Context())
	if !ok || staffID == 0 {
		response.JSON(w, http.StatusUnauthorized, nil, "unauthorized")
		return
	}

	profile, err := h.service.GetProfile(r.Context(), staffID)
	if err != nil {
		if errors.Is(err, ErrProfileNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, toProfileResponse(profile), "success")
}

// UpdateProfile handles PATCH /api/v1/profile.
// It updates the name and/or email of the authenticated staff user.
func (h *ProfileHandler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	staffID, ok := GetStaffIDFromContext(r.Context())
	if !ok || staffID == 0 {
		response.JSON(w, http.StatusUnauthorized, nil, "unauthorized")
		return
	}

	var req UpdateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	if req.Name == "" && req.Email == "" {
		response.JSON(w, http.StatusBadRequest, nil, "at least one field (name or email) must be provided")
		return
	}

	profile, err := h.service.UpdateProfile(r.Context(), UpdateProfileParams{
		ID:    staffID,
		Name:  req.Name,
		Email: req.Email,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrProfileNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrEmailAlreadyUsed):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		}
		return
	}

	response.JSON(w, http.StatusOK, toProfileResponse(profile), "profile updated")
}

// UpdatePassword handles PATCH /api/v1/profile/password.
// It updates the password of the authenticated staff user.
func (h *ProfileHandler) UpdatePassword(w http.ResponseWriter, r *http.Request) {
	staffID, ok := GetStaffIDFromContext(r.Context())
	if !ok || staffID == 0 {
		response.JSON(w, http.StatusUnauthorized, nil, "unauthorized")
		return
	}

	var req UpdatePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	if req.CurrentPassword == "" || req.NewPassword == "" {
		response.JSON(w, http.StatusBadRequest, nil, "current password and new password are required")
		return
	}

	if err := h.service.UpdatePassword(r.Context(), staffID, req.CurrentPassword, req.NewPassword); err != nil {
		switch {
		case errors.Is(err, ErrProfileNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrInvalidCredentials):
			response.JSON(w, http.StatusUnauthorized, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		}
		return
	}

	response.JSON(w, http.StatusOK, nil, "password updated successfully")
}

// toProfileResponse converts domain Profile to response DTO.
func toProfileResponse(p *Profile) ProfileResponse {
	return ProfileResponse{
		ID:        p.ID,
		RoleID:    p.RoleID,
		RoleName:  p.RoleName,
		Name:      p.Name,
		Email:     p.Email,
		IsActive:  p.IsActive,
		CreatedAt: p.CreatedAt.Format(time.RFC3339),
		UpdatedAt: p.UpdatedAt.Format(time.RFC3339),
	}
}
