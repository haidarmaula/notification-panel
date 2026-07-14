package users

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
)

// Domain errors.
var (
	ErrUserNotFound         = errors.New("user not found")
	ErrDeviceTokenNotFound  = errors.New("device token not found")
	ErrDeviceTokenDuplicate = errors.New("device token already exists")
	ErrInvalidPlatform      = errors.New("invalid platform: must be ANDROID, IOS, or WEB")
)

// UserService provides business logic for user management.
type UserService struct {
	userRepo     *repository.UserRepository
	deviceRepo   *repository.DeviceTokenRepository
	segmentRepo  *repository.SegmentMemberRepository
	deliveryRepo *repository.NotificationDeliveryRepository
	readRepo     *repository.NotificationReadRepository
}

// NewUserService creates a new UserService instance.
func NewUserService(
	userRepo *repository.UserRepository,
	deviceRepo *repository.DeviceTokenRepository,
	segmentRepo *repository.SegmentMemberRepository,
	deliveryRepo *repository.NotificationDeliveryRepository,
	readRepo *repository.NotificationReadRepository,
) *UserService {
	return &UserService{
		userRepo:     userRepo,
		deviceRepo:   deviceRepo,
		segmentRepo:  segmentRepo,
		deliveryRepo: deliveryRepo,
		readRepo:     readRepo,
	}
}

// ============================================
// User Methods
// ============================================

// List returns a paginated list of users with optional filters.
// Filters: keyword (search by name/email/external_id), status, external_id.
func (s *UserService) List(ctx context.Context, page, limit int32, keyword, status, externalID string) ([]User, int64, error) {
	offset := (page - 1) * limit

	var users []sqlc.User
	var total int64
	var err error

	// Fast path: exact external_id match.
	if externalID != "" {
		user, err := s.userRepo.FindByExternalID(ctx, externalID)
		if err != nil {
			return []User{}, 0, nil
		}
		users = []sqlc.User{user}
		total = 1
	} else if keyword != "" {
		users, err = s.userRepo.Search(ctx, keyword, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("search users: %w", err)
		}
		total, err = s.userRepo.Count(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("count users: %w", err)
		}
	} else if status != "" {
		users, err = s.userRepo.ListByStatus(ctx, status, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("list users by status: %w", err)
		}
		total, err = s.userRepo.CountByStatus(ctx, status)
		if err != nil {
			return nil, 0, fmt.Errorf("count users by status: %w", err)
		}
	} else {
		users, err = s.userRepo.List(ctx, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("list users: %w", err)
		}
		total, err = s.userRepo.Count(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("count users: %w", err)
		}
	}

	result := make([]User, len(users))
	for i, u := range users {
		result[i] = User{
			ID:         u.ID,
			ExternalID: u.ExternalID,
			Name:       u.Name.String,
			Email:      u.Email.String,
			Status:     u.Status,
			CreatedAt:  u.CreatedAt.Time,
			UpdatedAt:  u.UpdatedAt.Time,
		}
	}

	return result, total, nil
}

// GetByID returns a single user by ID.
func (s *UserService) GetByID(ctx context.Context, id int64) (*User, error) {
	user, err := s.userRepo.FindByID(ctx, id)
	if err != nil {
		return nil, ErrUserNotFound
	}

	return &User{
		ID:         user.ID,
		ExternalID: user.ExternalID,
		Name:       user.Name.String,
		Email:      user.Email.String,
		Status:     user.Status,
		CreatedAt:  user.CreatedAt.Time,
		UpdatedAt:  user.UpdatedAt.Time,
	}, nil
}

// Search returns a minimal list of users for autocomplete/typeahead.
func (s *UserService) Search(ctx context.Context, keyword string) ([]SearchUserResult, error) {
	users, err := s.userRepo.Search(ctx, keyword, 0, 20)
	if err != nil {
		return nil, fmt.Errorf("search users: %w", err)
	}

	result := make([]SearchUserResult, len(users))
	for i, u := range users {
		result[i] = SearchUserResult{
			ID:         u.ID,
			ExternalID: u.ExternalID,
			Name:       u.Name.String,
		}
	}

	return result, nil
}

// ============================================
// Device Token Methods
// ============================================

// ListDeviceTokens returns all device tokens for a user.
func (s *UserService) ListDeviceTokens(ctx context.Context, userID int64) ([]DeviceToken, error) {
	_, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return nil, ErrUserNotFound
	}

	rows, err := s.deviceRepo.ListByUser(ctx, userID, 0, 100)
	if err != nil {
		return nil, fmt.Errorf("list device tokens: %w", err)
	}

	result := make([]DeviceToken, len(rows))
	for i, row := range rows {
		var lastSeen *time.Time
		if row.LastSeenAt.Valid {
			lastSeen = &row.LastSeenAt.Time
		}
		result[i] = DeviceToken{
			ID:             row.ID,
			UserID:         row.UserID,
			Provider:       row.Provider,
			Platform:       row.Platform,
			InstallationID: row.InstallationID.String,
			PushToken:      row.PushToken,
			AppVersion:     row.AppVersion.String,
			OSVersion:      row.OsVersion.String,
			DeviceModel:    row.DeviceModel.String,
			IsActive:       row.IsActive,
			LastSeenAt:     lastSeen,
			CreatedAt:      row.CreatedAt.Time,
			UpdatedAt:      row.UpdatedAt.Time,
		}
	}

	return result, nil
}

// RegisterDeviceToken registers a new device token for a user.
func (s *UserService) RegisterDeviceToken(ctx context.Context, userID int64, req RegisterDeviceTokenRequest) (*DeviceToken, error) {
	// Validate platform.
	if req.Platform != "ANDROID" && req.Platform != "IOS" && req.Platform != "WEB" {
		return nil, ErrInvalidPlatform
	}

	// Check if user exists.
	_, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return nil, ErrUserNotFound
	}

	// Check for duplicate push_token.
	exists, err := s.deviceRepo.ExistsByPushToken(ctx, req.PushToken)
	if err != nil {
		return nil, fmt.Errorf("check push token: %w", err)
	}
	if exists {
		return nil, ErrDeviceTokenDuplicate
	}

	// Map provider based on platform.
	var provider string
	switch req.Platform {
	case "ANDROID":
		provider = "FCM"
	case "IOS":
		provider = "APNS"
	case "WEB":
		provider = "FCM"
	}

	var installationID, appVersion, osVersion, deviceModel pgtype.Text
	if req.InstallationID != nil {
		installationID = pgtype.Text{String: *req.InstallationID, Valid: true}
	}
	if req.AppVersion != nil {
		appVersion = pgtype.Text{String: *req.AppVersion, Valid: true}
	}
	if req.OSVersion != nil {
		osVersion = pgtype.Text{String: *req.OSVersion, Valid: true}
	}
	if req.DeviceModel != nil {
		deviceModel = pgtype.Text{String: *req.DeviceModel, Valid: true}
	}

	row, err := s.deviceRepo.Create(ctx, sqlc.CreateDeviceTokenParams{
		UserID:         userID,
		Platform:       req.Platform,
		InstallationID: installationID,
		PushToken:      req.PushToken,
		Provider:       provider,
		AppVersion:     appVersion,
		OsVersion:      osVersion,
		DeviceModel:    deviceModel,
	})
	if err != nil {
		return nil, fmt.Errorf("create device token: %w", err)
	}

	var lastSeen *time.Time
	if row.LastSeenAt.Valid {
		lastSeen = &row.LastSeenAt.Time
	}

	return &DeviceToken{
		ID:             row.ID,
		UserID:         row.UserID,
		Provider:       row.Provider,
		Platform:       row.Platform,
		InstallationID: row.InstallationID.String,
		PushToken:      row.PushToken,
		AppVersion:     row.AppVersion.String,
		OSVersion:      row.OsVersion.String,
		DeviceModel:    row.DeviceModel.String,
		IsActive:       row.IsActive,
		LastSeenAt:     lastSeen,
		CreatedAt:      row.CreatedAt.Time,
		UpdatedAt:      row.UpdatedAt.Time,
	}, nil
}

// UpdateDeviceToken updates a device token.
func (s *UserService) UpdateDeviceToken(ctx context.Context, tokenID int64, req UpdateDeviceTokenRequest) error {
	existing, err := s.deviceRepo.FindByID(ctx, tokenID)
	if err != nil {
		return ErrDeviceTokenNotFound
	}

	// Check duplicate push_token if changed.
	if req.PushToken != nil && *req.PushToken != existing.PushToken {
		exists, err := s.deviceRepo.ExistsByPushToken(ctx, *req.PushToken)
		if err != nil {
			return fmt.Errorf("check push token: %w", err)
		}
		if exists {
			return ErrDeviceTokenDuplicate
		}
	}

	// Build update params.
	update := sqlc.UpdateDeviceTokenFullParams{
		ID:             tokenID,
		Platform:       existing.Platform,
		InstallationID: existing.InstallationID,
		PushToken:      existing.PushToken,
		Provider:       existing.Provider,
		AppVersion:     existing.AppVersion,
		OsVersion:      existing.OsVersion,
		DeviceModel:    existing.DeviceModel,
	}

	if req.Platform != nil {
		if *req.Platform != "ANDROID" && *req.Platform != "IOS" && *req.Platform != "WEB" {
			return ErrInvalidPlatform
		}
		update.Platform = *req.Platform
		switch *req.Platform {
		case "ANDROID", "WEB":
			update.Provider = "FCM"
		case "IOS":
			update.Provider = "APNS"
		}
	}
	if req.PushToken != nil {
		update.PushToken = *req.PushToken
	}
	if req.InstallationID != nil {
		update.InstallationID = pgtype.Text{String: *req.InstallationID, Valid: true}
	}
	if req.AppVersion != nil {
		update.AppVersion = pgtype.Text{String: *req.AppVersion, Valid: true}
	}
	if req.OSVersion != nil {
		update.OsVersion = pgtype.Text{String: *req.OSVersion, Valid: true}
	}
	if req.DeviceModel != nil {
		update.DeviceModel = pgtype.Text{String: *req.DeviceModel, Valid: true}
	}

	if err := s.deviceRepo.UpdateFull(ctx, update); err != nil {
		return fmt.Errorf("update device token: %w", err)
	}

	if req.IsActive != nil {
		if err := s.deviceRepo.UpdateStatus(ctx, sqlc.UpdateDeviceTokenStatusParams{
			ID:       tokenID,
			IsActive: *req.IsActive,
		}); err != nil {
			return fmt.Errorf("update device token status: %w", err)
		}
	}

	return nil
}

// DeleteDeviceToken deletes a device token.
func (s *UserService) DeleteDeviceToken(ctx context.Context, tokenID int64) error {
	_, err := s.deviceRepo.FindByID(ctx, tokenID)
	if err != nil {
		return ErrDeviceTokenNotFound
	}
	return s.deviceRepo.Delete(ctx, tokenID)
}

// ============================================
// User Segments Methods
// ============================================

// GetUserSegments returns all segments a user belongs to.
func (s *UserService) GetUserSegments(ctx context.Context, userID int64) ([]UserSegment, error) {
	_, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return nil, ErrUserNotFound
	}

	rows, err := s.segmentRepo.ListSegmentsByUser(ctx, userID, 0, 100)
	if err != nil {
		return nil, fmt.Errorf("list segments by user: %w", err)
	}

	result := make([]UserSegment, len(rows))
	for i, row := range rows {
		result[i] = UserSegment{
			ID:   row.ID,
			Name: row.Name,
		}
	}

	return result, nil
}

// ============================================
// User Notification History Methods
// ============================================

// GetUserNotifications returns notification history for a user with pagination.
func (s *UserService) GetUserNotifications(ctx context.Context, userID int64, page, limit int32) ([]UserNotification, int64, error) {
	_, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return nil, 0, ErrUserNotFound
	}

	offset := (page - 1) * limit
	rows, err := s.deliveryRepo.ListByUser(ctx, userID, offset, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list user notifications: %w", err)
	}

	total, err := s.deliveryRepo.CountByUser(ctx, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("count user notifications: %w", err)
	}

	result := make([]UserNotification, len(rows))
	for i, row := range rows {
		var openedAt *string
		if row.OpenedAt.Valid {
			val := row.OpenedAt.Time.Format(time.RFC3339)
			openedAt = &val
		}
		result[i] = UserNotification{
			NotificationID: row.NotificationID,
			Title:          row.Title,
			Status:         row.Status,
			OpenedAt:       openedAt,
		}
	}

	return result, total, nil
}
