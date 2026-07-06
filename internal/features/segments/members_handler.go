package segments

import (
	"encoding/json"
	"errors"
	"net/http"

	"hello/pkg/response"
)

// MembersHandler handles HTTP requests for segment members.
type MembersHandler struct {
	service *MembersService
}

// NewMembersHandler creates a new MembersHandler instance.
func NewMembersHandler(service *MembersService) *MembersHandler {
	return &MembersHandler{service: service}
}

// ListMembers handles GET /api/v1/segments/{id}/members.
func (h *MembersHandler) ListMembers(w http.ResponseWriter, r *http.Request) {
	segmentID, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid segment id")
		return
	}

	page, limit := getPaginationParams(r)

	items, total, err := h.service.ListMembers(r.Context(), segmentID, page, limit)
	if err != nil {
		if errors.Is(err, ErrSegmentNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	resp := ListMembersResponse{
		Data: items,
		Pagination: Pagination{
			Page:  page,
			Limit: limit,
			Total: total,
		},
	}
	response.JSON(w, http.StatusOK, resp, "success")
}

// AddMember handles POST /api/v1/segments/{id}/members.
func (h *MembersHandler) AddMember(w http.ResponseWriter, r *http.Request) {
	segmentID, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid segment id")
		return
	}

	var req AddMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	if req.UserID == 0 {
		response.JSON(w, http.StatusBadRequest, nil, "user_id is required")
		return
	}

	err = h.service.AddMember(r.Context(), segmentID, req.UserID)
	if err != nil {
		switch {
		case errors.Is(err, ErrSegmentNotFound), errors.Is(err, ErrUserNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrMemberAlreadyExists):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		}
		return
	}

	response.JSON(w, http.StatusCreated, nil, "member added")
}

// RemoveMember handles DELETE /api/v1/segments/{id}/members/{userId}.
func (h *MembersHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	segmentID, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid segment id")
		return
	}

	userID, err := parseInt64FromPath(r, "userId")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid user id")
		return
	}

	err = h.service.RemoveMember(r.Context(), segmentID, userID)
	if err != nil {
		switch {
		case errors.Is(err, ErrSegmentNotFound), errors.Is(err, ErrUserNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrMemberNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		}
		return
	}

	response.JSON(w, http.StatusOK, nil, "member removed")
}
