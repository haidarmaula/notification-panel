package notifications

import "time"

// CreateNotificationRequest represents request payload for creating a notification.
type CreateNotificationRequest struct {
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	TemplateID  *int64     `json:"template_id,omitempty"`
	Type        string     `json:"type"` // BROADCAST, SEGMENT, INDIVIDUAL
	SegmentID   *int64     `json:"segment_id,omitempty"`
	UserIDs     []int64    `json:"user_ids,omitempty"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
}

// UpdateNotificationRequest represents request payload for updating a notification.
type UpdateNotificationRequest struct {
	Title       *string    `json:"title,omitempty"`
	Body        *string    `json:"body,omitempty"`
	TemplateID  *int64     `json:"template_id,omitempty"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
}

// NotificationListItem represents a notification in a list view.
type NotificationListItem struct {
	ID          int64      `json:"id"`
	Title       string     `json:"title"`
	Type        string     `json:"type"` // default "SEGMENT" because no join
	Status      string     `json:"status"`
	CreatedBy   string     `json:"created_by"`
	ScheduledAt *time.Time `json:"scheduled_at"`
	SentAt      *time.Time `json:"sent_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

// TemplateBrief represents a template in notification detail.
type TemplateBrief struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// StaffBrief represents a staff user in notification detail.
type StaffBrief struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// NotificationStatistics represents delivery statistics.
type NotificationStatistics struct {
	Targeted  int64 `json:"targeted"`
	Delivered int64 `json:"delivered"`
	Opened    int64 `json:"opened"`
}

// NotificationDetail represents full notification detail.
type NotificationDetail struct {
	ID          int64                  `json:"id"`
	Title       string                 `json:"title"`
	Body        string                 `json:"body"`
	Template    *TemplateBrief         `json:"template,omitempty"`
	Type        string                 `json:"type"`
	Status      string                 `json:"status"`
	CreatedBy   StaffBrief             `json:"created_by"`
	ScheduledAt *time.Time             `json:"scheduled_at"`
	SentAt      *time.Time             `json:"sent_at"`
	PublishedAt *time.Time             `json:"published_at"`
	CompletedAt *time.Time             `json:"completed_at"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
	Statistics  NotificationStatistics `json:"statistics"`
}

// ListNotificationsResponse represents paginated list response.
type ListNotificationsResponse struct {
	Data       []NotificationListItem `json:"data"`
	Pagination Pagination             `json:"pagination"`
}

// Pagination holds pagination metadata.
type Pagination struct {
	Page  int32 `json:"page"`
	Limit int32 `json:"limit"`
	Total int64 `json:"total"`
}

// CreateNotificationResponse represents response after creating a notification.
type CreateNotificationResponse struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}
