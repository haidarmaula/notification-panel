package staff

import (
	"encoding/json"
	"hello/pkg/response"
	"net/http"
)

type StaffHandler struct {
	service *StaffService
}

func NewStaffHandler(service *StaffService) *StaffHandler {
	return &StaffHandler{
		service: service,
	}
}

func (h *StaffHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateStaffUserRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	if req.Role == "" || req.Name == "" || req.Email == "" || req.Password == "" {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	staff, err := h.service.Create(r.Context(), CreateStaffUserParams{
		Role:     req.Role,
		Name:     req.Name,
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		response.JSON(w, http.StatusUnauthorized, nil, err.Error())
		return
	}

	response.JSON(w, http.StatusCreated, CreateStaffUserResponse{
		ID:        staff.ID,
		RoleID:    staff.RoleID,
		Name:      staff.Name,
		Email:     staff.Email,
		IsActive:  staff.IsActive,
		CreatedAt: staff.CreatedAt,
	}, "success")
}
