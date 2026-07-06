package segments

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"hello/internal/middleware"
	"hello/internal/token"
	"hello/pkg/response"
)

// SegmentHandler handles HTTP requests for segment management.
type SegmentHandler struct {
	service *SegmentService
}

// NewSegmentHandler creates a new SegmentHandler instance.
func NewSegmentHandler(service *SegmentService) *SegmentHandler {
	return &SegmentHandler{service: service}
}

// List handles GET /api/v1/segments.
// Query params: page, limit, search.
func (h *SegmentHandler) List(w http.ResponseWriter, r *http.Request) {
	page, limit := getPaginationParams(r)
	search := r.URL.Query().Get("search")

	items, total, err := h.service.List(r.Context(), page, limit, search)
	if err != nil {
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	resp := ListSegmentsResponse{
		Data: items,
		Pagination: Pagination{
			Page:  page,
			Limit: limit,
			Total: total,
		},
	}
	response.JSON(w, http.StatusOK, resp, "success")
}

// GetByID handles GET /api/v1/segments/{id}.
func (h *SegmentHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid segment id")
		return
	}

	detail, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrSegmentNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, detail, "success")
}

// Create handles POST /api/v1/segments.
func (h *SegmentHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateSegmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	if req.Name == "" {
		response.JSON(w, http.StatusBadRequest, nil, "name is required")
		return
	}

	staffID, ok := getStaffIDFromContext(r.Context())
	if !ok || staffID == 0 {
		response.JSON(w, http.StatusUnauthorized, nil, "unauthorized")
		return
	}

	result, err := h.service.Create(r.Context(), CreateParams{
		Name:        req.Name,
		Description: req.Description,
		CreatedBy:   staffID,
	})
	if err != nil {
		if errors.Is(err, ErrSegmentNameTaken) {
			response.JSON(w, http.StatusConflict, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	response.JSON(w, http.StatusCreated, result, "segment created")
}

// Update handles PATCH /api/v1/segments/{id}.
func (h *SegmentHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid segment id")
		return
	}

	var req UpdateSegmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	if req.Name == nil && req.Description == nil {
		response.JSON(w, http.StatusBadRequest, nil, "at least one field required")
		return
	}

	err = h.service.Update(r.Context(), UpdateParams{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrSegmentNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrSegmentNameTaken):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		}
		return
	}

	response.JSON(w, http.StatusOK, map[string]string{"message": "segment updated"}, "success")
}

// Delete handles DELETE /api/v1/segments/{id}.
func (h *SegmentHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid segment id")
		return
	}

	err = h.service.Delete(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, ErrSegmentNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrSegmentHasMembers):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		}
		return
	}

	response.JSON(w, http.StatusOK, nil, "deleted")
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
