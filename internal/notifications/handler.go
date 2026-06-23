package notifications

import (
	"encoding/json"
	"net/http"
	"strconv"

	"hello/pkg/response"
)

type NotificationHandler struct {
	service *NotificationService
}

func NewNotificationHandler(s *NotificationService) *NotificationHandler {
	return &NotificationHandler{service: s}
}

func (h *NotificationHandler) GetAll(w http.ResponseWriter, r *http.Request) {
	data := h.service.GetAll()
	response.JSON(w, http.StatusOK, data, "success")
}

func (h *NotificationHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid id")
		return
	}

	data, found := h.service.GetByID(id)
	if !found {
		response.JSON(w, http.StatusNotFound, nil, "not found")
		return
	}

	response.JSON(w, http.StatusOK, data, "success")
}

func (h *NotificationHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title   string `json:"title"`
		Message string `json:"message"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid body")
		return
	}

	data := h.service.Create(req.Title, req.Message)
	response.JSON(w, http.StatusCreated, data, "created")
}

func (h *NotificationHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid id")
		return
	}

	ok := h.service.Delete(id)
	if !ok {
		response.JSON(w, http.StatusNotFound, nil, "not found")
		return
	}

	response.JSON(w, http.StatusOK, nil, "deleted")
}
