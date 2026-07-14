package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

type DeviceTokenRepository struct {
	q sqlc.Querier
}

func NewDeviceTokenRepository(q sqlc.Querier) *DeviceTokenRepository {
	return &DeviceTokenRepository{q: q}
}

func (r *DeviceTokenRepository) CountByUser(ctx context.Context, userID int64) (int64, error) {
	return r.q.CountDeviceTokensByUser(ctx, userID)
}

func (r *DeviceTokenRepository) Create(ctx context.Context, params sqlc.CreateDeviceTokenParams) (sqlc.CreateDeviceTokenRow, error) {
	return r.q.CreateDeviceToken(ctx, params)
}

// UpdateFull updates all device token metadata including platform, app_version, etc.
func (r *DeviceTokenRepository) UpdateFull(ctx context.Context, params sqlc.UpdateDeviceTokenFullParams) error {
	return r.q.UpdateDeviceTokenFull(ctx, params)
}

func (r *DeviceTokenRepository) FindByID(ctx context.Context, id int64) (sqlc.DeviceToken, error) {
	return r.q.GetDeviceTokenByID(ctx, id)
}

func (r *DeviceTokenRepository) FindByPushToken(ctx context.Context, pushToken string) (sqlc.DeviceToken, error) {
	return r.q.GetDeviceTokenByPushToken(ctx, pushToken)
}

func (r *DeviceTokenRepository) ListByUser(ctx context.Context, userID int64, offset, limit int32) ([]sqlc.DeviceToken, error) {
	return r.q.ListDeviceTokensByUser(ctx, sqlc.ListDeviceTokensByUserParams{
		UserID: userID,
		Offset: offset,
		Limit:  limit,
	})
}

func (r *DeviceTokenRepository) Update(ctx context.Context, params sqlc.UpdateDeviceTokenParams) error {
	return r.q.UpdateDeviceToken(ctx, params)
}

func (r *DeviceTokenRepository) UpdateStatus(ctx context.Context, params sqlc.UpdateDeviceTokenStatusParams) error {
	return r.q.UpdateDeviceTokenStatus(ctx, params)
}

func (r *DeviceTokenRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteDeviceToken(ctx, id)
}

func (r *DeviceTokenRepository) ExistsByPushToken(ctx context.Context, pushToken string) (bool, error) {
	return r.q.ExistsDeviceToken(ctx, pushToken)
}
