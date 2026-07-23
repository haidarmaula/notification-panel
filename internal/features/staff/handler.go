package staff

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"hello/pkg/response"
)

// StaffHandler handles HTTP requests for staff management.
type StaffHandler struct {
	service *StaffService
}

// NewStaffHandler creates a new StaffHandler instance.
func NewStaffHandler(service *StaffService) *StaffHandler {
	return &StaffHandler{
		service: service,
	}
}

// Create handles POST /api/v1/staff.
// It validates the request, creates a new staff user, and returns 201 Created.
// Returns 400 for validation errors, 409 for duplicate email, and 500 for internal errors.
func (h *StaffHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateStaffRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}
	if req.Role == "" || req.Name == "" || req.Email == "" || req.Password == "" {
		response.JSON(w, http.StatusBadRequest, nil, "missing required fields")
		return
	}

	staff, err := h.service.Create(r.Context(), CreateStaffParams{
		Role:     req.Role,
		Name:     req.Name,
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrEmailAlreadyRegistered):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		case errors.Is(err, ErrInvalidRole):
			response.JSON(w, http.StatusBadRequest, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		}
		return
	}

	response.JSON(w, http.StatusCreated, toStaffResponse(staff), "staff created")
}

// GetByID handles GET /api/v1/staff/{id}.
// It retrieves a staff user by ID and returns 200 OK with staff data.
// Returns 404 if the staff user is not found.
func (h *StaffHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid id")
		return
	}
	staff, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrStaffNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	response.JSON(w, http.StatusOK, toStaffResponse(staff), "success")
}

// List handles GET /api/v1/staff.
// It returns a paginated list of staff users with optional search by name/email.
// Query params: page, limit, search.
func (h *StaffHandler) List(w http.ResponseWriter, r *http.Request) {
	page, limit := getPaginationParams(r)
	search := r.URL.Query().Get("search")

	items, total, err := h.service.List(r.Context(), page, limit, search)
	if err != nil {
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	data := make([]StaffResponse, len(items))
	for i, item := range items {
		data[i] = toStaffResponse(&item)
	}

	totalPages := int32((total + int64(limit) - 1) / int64(limit))
	resp := ListStaffResponse{
		Data:       data,
		Total:      total,
		Page:       page,
		Limit:      limit,
		TotalPages: totalPages,
	}
	response.JSON(w, http.StatusOK, resp, "success")
}

// Update handles PATCH /api/v1/staff/{id}.
// It updates a staff user's role, name, or email.
// Returns 200 OK with updated staff data, 404 if not found, 400 for invalid input.
func (h *StaffHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid id")
		return
	}
	var req UpdateStaffRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}
	if req.Role == "" && req.Name == "" && req.Email == "" {
		response.JSON(w, http.StatusBadRequest, nil, "at least one field must be provided")
		return
	}

	staff, err := h.service.Update(r.Context(), UpdateStaffParams{
		ID:    id,
		Role:  req.Role,
		Name:  req.Name,
		Email: req.Email,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrStaffNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrInvalidRole):
			response.JSON(w, http.StatusBadRequest, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		}
		return
	}
	response.JSON(w, http.StatusOK, toStaffResponse(staff), "staff updated")
}

// UpdateStatus handles PATCH /api/v1/staff/{id}/status.
// It updates the active status of a staff user.
// Returns 200 OK with updated staff data, 404 if not found.
func (h *StaffHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid id")
		return
	}
	var req UpdateStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}
	staff, err := h.service.UpdateStatus(r.Context(), id, req.IsActive)
	if err != nil {
		if errors.Is(err, ErrStaffNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	response.JSON(w, http.StatusOK, toStaffResponse(staff), "status updated")
}

// UpdatePassword handles PATCH /api/v1/staff/{id}/password.
// It updates the password of a staff user.
// Returns 200 OK on success, 404 if not found, 400 if password is empty.
func (h *StaffHandler) UpdatePassword(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid id")
		return
	}
	var req UpdatePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}
	if req.Password == "" {
		response.JSON(w, http.StatusBadRequest, nil, "password required")
		return
	}
	if err := h.service.UpdatePassword(r.Context(), id, req.Password); err != nil {
		if errors.Is(err, ErrStaffNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	response.JSON(w, http.StatusOK, nil, "password updated")
}

// DELETE /staff/{id}
func (h *StaffHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid id")
		return
	}
	if err = h.service.Delete(r.Context(), id); err != nil {
		if errors.Is(err, ErrStaffNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}
	response.JSON(w, http.StatusOK, nil, "staff deleted")
}

// toStaffResponse converts a domain Staff object to a StaffResponse DTO.
func toStaffResponse(s *Staff) StaffResponse {
	return StaffResponse{
		ID:        s.ID,
		RoleID:    s.RoleID,
		RoleName:  s.RoleName,
		Name:      s.Name,
		Email:     s.Email,
		IsActive:  s.IsActive,
		CreatedAt: s.CreatedAt.Format(time.RFC3339),
		UpdatedAt: s.UpdatedAt.Format(time.RFC3339),
	}
}

// parseInt64FromPath extracts and parses an int64 from a URL path parameter.
func parseInt64FromPath(r *http.Request, key string) (int64, error) {
	raw := r.PathValue(key)
	if raw == "" {
		return 0, errors.New("missing id")
	}
	return strconv.ParseInt(raw, 10, 64)
}

// getPaginationParams extracts page and limit from query parameters with defaults.
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
