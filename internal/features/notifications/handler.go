package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"hello/internal/kafka"
	"hello/internal/middleware"
	"hello/internal/token"
	"hello/pkg/response"
)

type contextKeyStaffID struct{}

// NotificationHandler handles HTTP requests for notifications.
type NotificationHandler struct {
	service  *NotificationService
	producer *kafka.Producer
}

// NewNotificationHandler creates a new NotificationHandler instance.
func NewNotificationHandler(service *NotificationService, producer *kafka.Producer) *NotificationHandler {
	return &NotificationHandler{
		service:  service,
		producer: producer,
	}
}

// List handles GET /api/v1/notifications.
// Query params: page, limit, status, type (target type), keyword.
func (h *NotificationHandler) List(w http.ResponseWriter, r *http.Request) {
	page, limit := getPaginationParams(r)
	status := r.URL.Query().Get("status")
	targetType := r.URL.Query().Get("type")
	keyword := r.URL.Query().Get("keyword")

	items, total, err := h.service.List(r.Context(), page, limit, status, targetType, keyword)
	if err != nil {
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	resp := ListNotificationsResponse{
		Data: items,
		Pagination: Pagination{
			Page:  page,
			Limit: limit,
			Total: total,
		},
	}
	response.JSON(w, http.StatusOK, resp, "success")
}

// GetByID handles GET /api/v1/notifications/{id}.
func (h *NotificationHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid id")
		return
	}

	detail, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotificationNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, detail, "success")
}

// Create handles POST /api/v1/notifications.
func (h *NotificationHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateNotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	if req.Title == "" || req.Body == "" {
		response.JSON(w, http.StatusBadRequest, nil, "title and body required")
		return
	}

	staffID, ok := getStaffIDFromContext(r.Context())
	if !ok || staffID == 0 {
		response.JSON(w, http.StatusUnauthorized, nil, "unauthorized")
		return
	}

	if req.Type == "" {
		req.Type = "BROADCAST"
	}

	if req.Type == string(TargetIndividual) && len(req.UserIDs) == 0 {
		response.JSON(w, http.StatusBadRequest, nil, "user_ids required for INDIVIDUAL type")
		return
	}
	if req.Type == string(TargetSegment) && req.SegmentID == nil {
		response.JSON(w, http.StatusBadRequest, nil, "segment_id required for SEGMENT type")
		return
	}

	result, err := h.service.Create(r.Context(), CreateParams{
		Title:       req.Title,
		Body:        req.Body,
		TemplateID:  req.TemplateID,
		TargetType:  req.Type,
		SegmentID:   req.SegmentID,
		UserIDs:     req.UserIDs,
		ScheduledAt: req.ScheduledAt,
		CreatedBy:   staffID,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidTargetType), errors.Is(err, ErrTemplateNotFound),
			errors.Is(err, ErrSegmentNotFound), errors.Is(err, ErrInvalidScheduledTime),
			errors.Is(err, ErrTargetsRequired):
			response.JSON(w, http.StatusBadRequest, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		}
		return
	}

	response.JSON(w, http.StatusCreated, result, "created")
}

// Update handles PATCH /api/v1/notifications/{id}.
func (h *NotificationHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid id")
		return
	}

	var req UpdateNotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	if req.Title == nil && req.Body == nil && req.TemplateID == nil && req.ScheduledAt == nil {
		response.JSON(w, http.StatusBadRequest, nil, "at least one field required")
		return
	}

	err = h.service.Update(r.Context(), id, UpdateParams{
		Title:       req.Title,
		Body:        req.Body,
		TemplateID:  req.TemplateID,
		ScheduledAt: req.ScheduledAt,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrNotificationNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrNotificationNotDraft):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		case errors.Is(err, ErrTemplateNotFound), errors.Is(err, ErrInvalidScheduledTime):
			response.JSON(w, http.StatusBadRequest, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		}
		return
	}

	response.JSON(w, http.StatusOK, map[string]string{"message": "notification updated"}, "success")
}

// Delete handles DELETE /api/v1/notifications/{id}.
func (h *NotificationHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid id")
		return
	}

	err = h.service.Delete(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotificationNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrCannotDeleteSent):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		}
		return
	}

	response.JSON(w, http.StatusOK, nil, "deleted")
}

// Send handles POST /api/v1/notifications/{id}/send.
// It publishes a send requested event to Kafka.
func (h *NotificationHandler) Send(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid notification id")
		return
	}

	// Check if notification exists and is in DRAFT status
	notif, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotificationNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	if notif.Status != "DRAFT" {
		response.JSON(w, http.StatusBadRequest, nil, "notification must be in DRAFT status")
		return
	}

	// Publish event to Kafka
	event := kafka.NotificationSendRequested{
		NotificationID: notif.ID,
		RequestedAt:    time.Now(),
	}
	if err := h.producer.PublishSendRequested(r.Context(), event); err != nil {
		response.JSON(w, http.StatusInternalServerError, nil, "failed to queue notification")
		return
	}

	// Update notification status to SENDING (optional, or wait for worker to update)
	// For now, we just return success.
	response.JSON(w, http.StatusAccepted, map[string]string{"message": "notification queued for sending"}, "success")
}

// parseInt64FromPath extracts and parses int64 from URL path parameter.
func parseInt64FromPath(r *http.Request, key string) (int64, error) {
	raw := r.PathValue(key)
	if raw == "" {
		return 0, errors.New("missing id")
	}
	return strconv.ParseInt(raw, 10, 64)
}

// getPaginationParams extracts page and limit from query parameters.
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

// getStaffIDFromContext retrieves staff ID from context.
func getStaffIDFromContext(ctx context.Context) (int64, bool) {
	claims, ok := ctx.Value(middleware.UserContextKey).(*token.AccessClaims)
	if !ok || claims == nil {
		return 0, false
	}
	return claims.StaffID, true

}
