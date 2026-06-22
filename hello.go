package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// ===
// DTO
// ===
type APIResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
}

func JSON(
	w http.ResponseWriter,
	status int,
	data any,
	message string,
) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	response := APIResponse{
		Success: status < 400,
		Message: message,
		Data:    data,
	}

	json.NewEncoder(w).Encode(response)
}

// =======
// STRUCTS
// =======
type Hello struct {
	Message string `json:"message"`
}

type Notification struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Message string `json:"message"`
}

// ===============
// GLOBAL VARIABLE
// ===============
var notifications = []Notification{
	{
		ID:      1,
		Title:   "Promo",
		Message: "Diskon 50%",
	},
	{
		ID:      2,
		Title:   "Promo",
		Message: "Diskon 35%",
	},
}

var nextID = len(notifications)

// ========
// HANDLERS
// ========
func getHello(w http.ResponseWriter, r *http.Request) {
	hello := Hello{
		Message: "hello",
	}
	JSON(w, http.StatusOK, hello, "success")
}

func getNotifications(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, notifications, "success")
}

func getNotificationByID(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)

	if err != nil {
		JSON(w, http.StatusBadRequest, nil, "invalid ID")
		return
	}

	for _, notification := range notifications {
		if notification.ID == id {
			JSON(
				w,
				http.StatusOK,
				notification,
				"notification found",
			)
			return
		}
	}

	JSON(w, http.StatusNotFound, nil, "notification not found")
}

func postNotifications(w http.ResponseWriter, r *http.Request) {
	var notification Notification

	err := json.NewDecoder(r.Body).Decode(&notification)

	if err != nil {
		JSON(w, http.StatusBadRequest, nil, err.Error())
		return
	}

	notification.ID = int64(nextID)
	nextID++
	notifications = append(notifications, notification)

	JSON(w, http.StatusCreated, notification, "notification created")
}

func deleteNotificationByID(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)

	if err != nil {
		JSON(w, http.StatusBadRequest, nil, "invalid ID")
		return
	}

	for i, notification := range notifications {
		if notification.ID == id {
			notifications = append(notifications[:i], notifications[i+1:]...)
			JSON(
				w,
				http.StatusOK,
				nil,
				"notification deleted",
			)
			return
		}
	}

	JSON(w, http.StatusNotFound, nil, "notification not found")
}

// ==========
// MIDDLEWARE
// ==========
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		fmt.Println(r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /", getHello)

	mux.HandleFunc("GET /notifications", getNotifications)
	mux.HandleFunc("GET /notifications/{id}", getNotificationByID)
	mux.HandleFunc("POST /notifications", postNotifications)
	mux.HandleFunc("DELETE /notifications/{id}", deleteNotificationByID)

	fmt.Println("server running on port 8080")
	http.ListenAndServe(":8080", Logging(mux))
}
