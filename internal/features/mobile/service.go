package mobile

import (
	"context"
	"errors"
	"fmt"

	"hello/internal/database/repository"
	"hello/internal/database/sqlc"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Domain errors.
var (
	ErrInvalidJWT      = errors.New("invalid or expired JWT")
	ErrInvalidPlatform = errors.New("invalid platform: must be ANDROID, IOS, or WEB")
	ErrUserNotFound    = errors.New("user not found")
)

// MobileService provides business logic for mobile app integration.
type MobileService struct {
	userRepo   *repository.UserRepository
	deviceRepo *repository.DeviceTokenRepository
	jwtSecret  string
}

// NewMobileService creates a new MobileService instance.
func NewMobileService(
	userRepo *repository.UserRepository,
	deviceRepo *repository.DeviceTokenRepository,
	jwtSecret string,
) *MobileService {
	return &MobileService{
		userRepo:   userRepo,
		deviceRepo: deviceRepo,
		jwtSecret:  jwtSecret,
	}
}

// SyncParams holds input for syncing user and device.
type SyncParams struct {
	JWT            string
	Platform       string
	PushToken      string
	InstallationID *string
	AppVersion     *string
	OSVersion      *string
	DeviceModel    *string
}

// Sync synchronizes a user from JWT and registers their device token.
func (s *MobileService) Sync(ctx context.Context, params SyncParams) (*MobileSyncResult, error) {
	// 1. Verify and parse JWT
	claims, err := s.verifyJWT(params.JWT)
	if err != nil {
		return nil, ErrInvalidJWT
	}

	// 2. Upsert user
	user, err := s.upsertUser(ctx, claims)
	if err != nil {
		return nil, fmt.Errorf("upsert user: %w", err)
	}

	// 3. Validate platform
	if params.Platform != "ANDROID" && params.Platform != "IOS" && params.Platform != "WEB" {
		return nil, ErrInvalidPlatform
	}

	// 4. Register device token
	device, err := s.registerDeviceToken(ctx, user.ID, params)
	if err != nil {
		return nil, fmt.Errorf("register device token: %w", err)
	}

	return &MobileSyncResult{
		UserID:        user.ID,
		DeviceTokenID: device.ID,
	}, nil
}

// verifyJWT verifies and parses the JWT token.
func (s *MobileService) verifyJWT(tokenString string) (*MobileClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &MobileClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.jwtSecret), nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*MobileClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	return claims, nil
}

// upsertUser creates or updates a user based on external_id from JWT.
func (s *MobileService) upsertUser(ctx context.Context, claims *MobileClaims) (*sqlc.User, error) {
	// Try to find by external_id
	existing, err := s.userRepo.FindByExternalID(ctx, claims.ExternalID)
	if err == nil {
		// User exists: update if any field changed
		if existing.Name.String != claims.Name || existing.Email.String != claims.Email {
			update := sqlc.UpdateUserParams{
				ID:     existing.ID,
				Name:   pgtype.Text{String: claims.Name, Valid: true},
				Email:  pgtype.Text{String: claims.Email, Valid: true},
				Status: existing.Status,
			}
			if err := s.userRepo.Update(ctx, update); err != nil {
				return nil, err
			}
			// Fetch updated user
			updated, err := s.userRepo.FindByID(ctx, existing.ID)
			if err != nil {
				return nil, err
			}
			return &updated, nil
		}
		return &existing, nil
	}

	// User does not exist: create new
	newUser, err := s.userRepo.Create(ctx, sqlc.CreateUserParams{
		ExternalID: claims.ExternalID,
		Name:       pgtype.Text{String: claims.Name, Valid: true},
		Email:      pgtype.Text{String: claims.Email, Valid: true},
		Status:     "ACTIVE",
	})
	if err != nil {
		return nil, err
	}
	return &newUser, nil
}

// registerDeviceToken registers or updates a device token for a user.
func (s *MobileService) registerDeviceToken(
	ctx context.Context,
	userID int64,
	params SyncParams,
) (*sqlc.DeviceToken, error) {
	// Determine provider based on platform
	var provider string
	switch params.Platform {
	case "ANDROID":
		provider = "FCM"
	case "IOS":
		provider = "APNS"
	case "WEB":
		provider = "FCM"
	}

	// Prepare optional fields
	var installationID, appVersion, osVersion, deviceModel pgtype.Text
	if params.InstallationID != nil {
		installationID = pgtype.Text{String: *params.InstallationID, Valid: true}
	}
	if params.AppVersion != nil {
		appVersion = pgtype.Text{String: *params.AppVersion, Valid: true}
	}
	if params.OSVersion != nil {
		osVersion = pgtype.Text{String: *params.OSVersion, Valid: true}
	}
	if params.DeviceModel != nil {
		deviceModel = pgtype.Text{String: *params.DeviceModel, Valid: true}
	}

	// Check if token already exists
	exists, err := s.deviceRepo.ExistsByPushToken(ctx, params.PushToken)
	if err != nil {
		return nil, err
	}

	if exists {
		// Find and update existing token
		existingToken, err := s.deviceRepo.FindByPushToken(ctx, params.PushToken)
		if err != nil {
			return nil, err
		}

		update := sqlc.UpdateDeviceTokenFullParams{
			ID:             existingToken.ID,
			Platform:       params.Platform,
			InstallationID: installationID,
			PushToken:      params.PushToken,
			Provider:       provider,
			AppVersion:     appVersion,
			OsVersion:      osVersion,
			DeviceModel:    deviceModel,
		}
		if err := s.deviceRepo.UpdateFull(ctx, update); err != nil {
			return nil, err
		}

		// Fetch updated token
		updated, err := s.deviceRepo.FindByID(ctx, existingToken.ID)
		if err != nil {
			return nil, err
		}
		return &updated, nil
	}

	// Create new token
	newToken, err := s.deviceRepo.Create(ctx, sqlc.CreateDeviceTokenParams{
		UserID:         userID,
		Platform:       params.Platform,
		InstallationID: installationID,
		PushToken:      params.PushToken,
		Provider:       provider,
		AppVersion:     appVersion,
		OsVersion:      osVersion,
		DeviceModel:    deviceModel,
	})
	if err != nil {
		return nil, err
	}
	return &newToken, nil
}
