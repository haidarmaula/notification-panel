package mobile

import "time"

// MobileUser represents the domain model for a mobile user.
type MobileUser struct {
	ID         int64
	ExternalID string
	Name       string
	Email      string
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// MobileDevice represents the domain model for a mobile device token.
type MobileDevice struct {
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

// MobileSyncResult represents the result of a sync operation.
type MobileSyncResult struct {
	UserID        int64
	DeviceTokenID int64
}
