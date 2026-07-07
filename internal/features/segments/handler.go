package segments

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

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
func (h *SegmentHandler) List(w http.ResponseWriter, r *http.Request) {
	page, limit := getPaginationParams(r)
	search := r.URL.Query().Get("search")

	segments, total, err := h.service.List(r.Context(), page, limit, search)
	if err != nil {
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	// Map domain to response DTOs
	data := make([]SegmentListItem, len(segments))
	for i, s := range segments {
		data[i] = SegmentListItem{
			ID:          s.ID,
			Name:        s.Name,
			Description: &s.Description,
			CreatedBy:   "", // will be filled by fetching staff name, but we can leave empty or fetch in loop
			MemberCount: s.MemberCount,
			CreatedAt:   s.CreatedAt,
			UpdatedAt:   s.UpdatedAt,
		}
	}

	resp := ListSegmentsResponse{
		Data: data,
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

	segmentWithCount, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrSegmentNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	// Fetch staff name
	staff, err := h.service.staffRepo.FindByID(r.Context(), segmentWithCount.CreatedBy)
	staffName := ""
	if err == nil {
		staffName = staff.Name
	}

	detail := SegmentDetail{
		ID:          segmentWithCount.ID,
		Name:        segmentWithCount.Name,
		Description: &segmentWithCount.Description,
		CreatedBy: StaffBrief{
			ID:   segmentWithCount.CreatedBy,
			Name: staffName,
		},
		MemberCount: segmentWithCount.MemberCount,
		CreatedAt:   segmentWithCount.CreatedAt,
		UpdatedAt:   segmentWithCount.UpdatedAt,
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

	segment, err := h.service.Create(r.Context(), CreateParams{
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

	resp := CreateSegmentResponse{ID: segment.ID}
	response.JSON(w, http.StatusCreated, resp, "segment created")
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

	updated, err := h.service.Update(r.Context(), UpdateParams{
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

	response.JSON(w, http.StatusOK, map[string]string{
		"message": "segment updated",
		"id":      strconv.FormatInt(updated.ID, 10),
	}, "success")
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
