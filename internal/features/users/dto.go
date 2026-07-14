package users

// ============================================
// User DTOs
// ============================================

// UserResponse represents a user in API responses.
type UserResponse struct {
	ID         int64  `json:"id"`
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

// ListUsersResponse represents paginated list of users.
type ListUsersResponse struct {
	Data       []UserResponse `json:"data"`
	Pagination Pagination     `json:"pagination"`
}

// SearchUserResult represents a user in search results (minimal fields).
type SearchUserResult struct {
	ID         int64  `json:"id"`
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
}

// ============================================
// Device Token DTOs
// ============================================

// DeviceTokenResponse represents a device token in API responses.
type DeviceTokenResponse struct {
	ID             int64   `json:"id"`
	Platform       string  `json:"platform"`
	InstallationID string  `json:"installation_id,omitempty"`
	IsActive       bool    `json:"is_active"`
	LastSeenAt     *string `json:"last_seen_at,omitempty"`
	CreatedAt      string  `json:"created_at"`
}

// ListDeviceTokensResponse represents list of device tokens for a user.
type ListDeviceTokensResponse struct {
	Data []DeviceTokenResponse `json:"data"`
}

// ============================================
// User Segments DTO
// ============================================

// UserSegmentResponse represents a segment in user's membership.
type UserSegmentResponse struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// ListUserSegmentsResponse represents list of segments a user belongs to.
type ListUserSegmentsResponse struct {
	Data []UserSegmentResponse `json:"data"`
}

// ============================================
// User Notification History DTO
// ============================================

// UserNotificationHistoryItem represents a notification in user's history.
type UserNotificationHistoryItem struct {
	NotificationID int64   `json:"notification_id"`
	Title          string  `json:"title"`
	Status         string  `json:"status"`
	OpenedAt       *string `json:"opened_at,omitempty"`
}

// ListUserNotificationsResponse represents paginated notification history.
type ListUserNotificationsResponse struct {
	Data       []UserNotificationHistoryItem `json:"data"`
	Pagination Pagination                    `json:"pagination"`
}

// ============================================
// Device Token Registration DTOs (Mobile API)
// ============================================

// RegisterDeviceTokenRequest represents request to register a device token.
type RegisterDeviceTokenRequest struct {
	Platform       string  `json:"platform"` // ANDROID, IOS, WEB
	PushToken      string  `json:"push_token"`
	InstallationID *string `json:"installation_id,omitempty"`
	AppVersion     *string `json:"app_version,omitempty"`
	OSVersion      *string `json:"os_version,omitempty"`
	DeviceModel    *string `json:"device_model,omitempty"`
}

// UpdateDeviceTokenRequest represents request to update a device token.
type UpdateDeviceTokenRequest struct {
	Platform       *string `json:"platform,omitempty"`
	PushToken      *string `json:"push_token,omitempty"`
	InstallationID *string `json:"installation_id,omitempty"`
	AppVersion     *string `json:"app_version,omitempty"`
	OSVersion      *string `json:"os_version,omitempty"`
	DeviceModel    *string `json:"device_model,omitempty"`
	IsActive       *bool   `json:"is_active,omitempty"`
}

// ============================================
// Shared Pagination
// ============================================

// Pagination holds pagination metadata.
type Pagination struct {
	Page  int32 `json:"page"`
	Limit int32 `json:"limit"`
	Total int64 `json:"total"`
}
