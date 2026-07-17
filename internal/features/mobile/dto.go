package mobile

// DeviceTokenRequest represents the device token part of the sync request.
type DeviceTokenRequest struct {
	Platform       string  `json:"platform"`
	PushToken      string  `json:"push_token"`
	InstallationID *string `json:"installation_id,omitempty"`
	AppVersion     *string `json:"app_version,omitempty"`
	OSVersion      *string `json:"os_version,omitempty"`
	DeviceModel    *string `json:"device_model,omitempty"`
}

// SyncRequest represents the full sync request payload.
type SyncRequest struct {
	JWT         string             `json:"jwt"`
	DeviceToken DeviceTokenRequest `json:"device_token"`
}

// SyncResponse represents the response after sync.
type SyncResponse struct {
	UserID        int64 `json:"user_id"`
	DeviceTokenID int64 `json:"device_token_id"`
}
