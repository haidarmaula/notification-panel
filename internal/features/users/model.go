package users

import "time"

// User represents the domain model for a mobile application user.
type User struct {
	ID         int64
	ExternalID string
	Name       string
	Email      string
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// DeviceToken represents the domain model for a device token.
type DeviceToken struct {
	ID             int64
	UserID         int64
	Provider       string
	Platform       string
	InstallationID string
	PushToken      string
	AppVersion     string
	OSVersion      string
	DeviceModel    string
	IsActive       bool
	LastSeenAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// UserSegment represents a segment that a user belongs to.
type UserSegment struct {
	ID   int64
	Name string
}

// UserNotification represents a notification in user's history.
type UserNotification struct {
	NotificationID int64
	Title          string
	Status         string
	OpenedAt       *string
}
