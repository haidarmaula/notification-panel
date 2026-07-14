package users

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"hello/internal/middleware"
	"hello/internal/token"
	"hello/pkg/response"
)

// UserHandler handles HTTP requests for user management.
type UserHandler struct {
	service *UserService
}

// NewUserHandler creates a new UserHandler instance.
func NewUserHandler(service *UserService) *UserHandler {
	return &UserHandler{service: service}
}

// List handles GET /api/v1/users.
// Query params: page, limit, keyword, status, external_id.
func (h *UserHandler) List(w http.ResponseWriter, r *http.Request) {
	page, limit := getPaginationParams(r)
	keyword := r.URL.Query().Get("keyword")
	status := r.URL.Query().Get("status")
	externalID := r.URL.Query().Get("external_id")

	users, total, err := h.service.List(r.Context(), page, limit, keyword, status, externalID)
	if err != nil {
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	data := make([]UserResponse, len(users))
	for i, u := range users {
		data[i] = UserResponse{
			ID:         u.ID,
			ExternalID: u.ExternalID,
			Name:       u.Name,
			Email:      u.Email,
			Status:     u.Status,
			CreatedAt:  u.CreatedAt.Format(time.RFC3339),
			UpdatedAt:  u.UpdatedAt.Format(time.RFC3339),
		}
	}

	resp := ListUsersResponse{
		Data: data,
		Pagination: Pagination{
			Page:  page,
			Limit: limit,
			Total: total,
		},
	}
	response.JSON(w, http.StatusOK, resp, "success")
}

// GetByID handles GET /api/v1/users/{id}.
func (h *UserHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid user id")
		return
	}

	user, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	resp := UserResponse{
		ID:         user.ID,
		ExternalID: user.ExternalID,
		Name:       user.Name,
		Email:      user.Email,
		Status:     user.Status,
		CreatedAt:  user.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  user.UpdatedAt.Format(time.RFC3339),
	}
	response.JSON(w, http.StatusOK, resp, "success")
}

// Search handles GET /api/v1/users/search for autocomplete.
// Query param: keyword (required).
func (h *UserHandler) Search(w http.ResponseWriter, r *http.Request) {
	keyword := r.URL.Query().Get("keyword")
	if keyword == "" {
		response.JSON(w, http.StatusBadRequest, nil, "keyword is required")
		return
	}

	results, err := h.service.Search(r.Context(), keyword)
	if err != nil {
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, results, "success")
}

// GetUserSegments handles GET /api/v1/users/{id}/segments.
func (h *UserHandler) GetUserSegments(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid user id")
		return
	}

	segments, err := h.service.GetUserSegments(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	data := make([]UserSegmentResponse, len(segments))
	for i, s := range segments {
		data[i] = UserSegmentResponse{
			ID:   s.ID,
			Name: s.Name,
		}
	}
	response.JSON(w, http.StatusOK, ListUserSegmentsResponse{Data: data}, "success")
}

// GetUserNotifications handles GET /api/v1/users/{id}/notifications.
// Query params: page, limit.
func (h *UserHandler) GetUserNotifications(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid user id")
		return
	}

	page, limit := getPaginationParams(r)

	notifications, total, err := h.service.GetUserNotifications(r.Context(), id, page, limit)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	data := make([]UserNotificationHistoryItem, len(notifications))
	for i, n := range notifications {
		data[i] = UserNotificationHistoryItem{
			NotificationID: n.NotificationID,
			Title:          n.Title,
			Status:         n.Status,
			OpenedAt:       n.OpenedAt,
		}
	}

	resp := ListUserNotificationsResponse{
		Data: data,
		Pagination: Pagination{
			Page:  page,
			Limit: limit,
			Total: total,
		},
	}
	response.JSON(w, http.StatusOK, resp, "success")
}

// ============================================
// Helper Functions
// ============================================

func parseInt64FromPath(r *http.Request, key string) (int64, error) {
	raw := r.PathValue(key)
	if raw == "" {
		return 0, errors.New("missing id")
	}
	return strconv.ParseInt(raw, 10, 64)
}

func getPaginationParams(r *http.Request) (page, limit int32) {
	page = 1
	limit = 10
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.ParseInt(p, 10, 32); err == nil && v > 0 {
			page = int32(v)
		}
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.ParseInt(l, 10, 32); err == nil && v > 0 && v <= 100 {
			limit = int32(v)
		}
	}
	return
}

func getStaffIDFromContext(ctx context.Context) (int64, bool) {
	claims, ok := ctx.Value(middleware.UserContextKey).(*token.AccessClaims)
	if !ok || claims == nil {
		return 0, false
	}
	return claims.StaffID, true
}
