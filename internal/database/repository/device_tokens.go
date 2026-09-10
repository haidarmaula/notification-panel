package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

// DeviceTokenRepository provides read and write access to the device_tokens
// table.
//
// A device token represents a single (user, installation) pair — the same
// user may have many device tokens (phone, tablet, reinstall), and the same
// push token may in principle be reassigned if a device is wiped and
// re-registered. The repository exposes both directions of that lookup
// (FindByPushToken and ListByUser) because the mobile registration flow needs
// to upsert by push token while the delivery pipeline needs to fan out by
// user.
type DeviceTokenRepository struct {
	q sqlc.Querier
}

// NewDeviceTokenRepository constructs a DeviceTokenRepository bound to q.
//
// q must be non-nil. As with the other constructors in this package, the
// typical caller passes sqlc.New(db) but a transaction-scoped querier or a
// test fake is equally valid.
func NewDeviceTokenRepository(q sqlc.Querier) *DeviceTokenRepository {
	return &DeviceTokenRepository{q: q}
}

// CountByUser returns the number of device tokens registered by a single
// user. The count includes both active and inactive tokens unless the
// underlying query filters on is_active; consult the SQL definition in
// db/queries/device_tokens.sql for the exact predicate.
func (r *DeviceTokenRepository) CountByUser(ctx context.Context, userID int64) (int64, error) {
	return r.q.CountDeviceTokensByUser(ctx, userID)
}

// Create inserts a new device token and returns the full DeviceToken model.
//
// The sqlc-generated CreateDeviceToken query returns a narrower
// CreateDeviceTokenRow projection; the repository normalises it to the
// canonical sqlc.DeviceToken type so that callers deal with a single model
// regardless of which method produced it. If the underlying table enforces a
// uniqueness constraint on push_token (which it should, for idempotent
// registration), a duplicate insert surfaces as a pgx error.
func (r *DeviceTokenRepository) Create(ctx context.Context, params sqlc.CreateDeviceTokenParams) (sqlc.DeviceToken, error) {
	row, err := r.q.CreateDeviceToken(ctx, params)
	if err != nil {
		return sqlc.DeviceToken{}, err
	}
	// Convert from CreateDeviceTokenRow to DeviceToken
	return sqlc.DeviceToken{
		ID:             row.ID,
		UserID:         row.UserID,
		Provider:       row.Provider,
		Platform:       row.Platform,
		InstallationID: row.InstallationID,
		PushToken:      row.PushToken,
		AppVersion:     row.AppVersion,
		OsVersion:      row.OsVersion,
		DeviceModel:    row.DeviceModel,
		IsActive:       row.IsActive,
		LastSeenAt:     row.LastSeenAt,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}, nil
}

// UpdateFull updates all device token metadata including platform, app_version, etc.
//
// It is intended for the mobile-app "sync" flow, where the client sends the
// complete device state on every launch. Because it overwrites every mutable
// column, callers must supply a fully-populated params struct — partial
// updates will null out fields that were omitted. For targeted updates, use
// Update (metadata subset) or UpdateStatus (is_active only) instead.
func (r *DeviceTokenRepository) UpdateFull(ctx context.Context, params sqlc.UpdateDeviceTokenFullParams) error {
	return r.q.UpdateDeviceTokenFull(ctx, params)
}

// FindByID retrieves a device token by its primary key.
//
// If no row matches id, pgx.ErrNoRows is returned unchanged. Callers
// translate this to their domain's not-found error as appropriate.
func (r *DeviceTokenRepository) FindByID(ctx context.Context, id int64) (sqlc.DeviceToken, error) {
	return r.q.GetDeviceTokenByID(ctx, id)
}

// FindByPushToken retrieves a device token by its push token string.
//
// Push tokens are the natural lookup key during mobile registration, where
// the client provides only the opaque token issued by FCM or APNs. The
// underlying query returns pgx.ErrNoRows when the token is unknown, which the
// repository passes through unchanged. If the schema permits multiple rows
// per push token (for example, because a device was re-registered to a
// different user), the query semantics — first row vs error — are defined by
// the SQL, not by this wrapper.
func (r *DeviceTokenRepository) FindByPushToken(ctx context.Context, pushToken string) (sqlc.DeviceToken, error) {
	return r.q.GetDeviceTokenByPushToken(ctx, pushToken)
}

// ListByUser returns a page of device tokens belonging to a single user,
// using the supplied offset and limit for pagination.
//
// offset and limit are passed through to SQL verbatim; the repository
// performs no bounds checking or defaulting. Callers that need deterministic
// ordering should not assume one — consult the SQL for the ORDER BY clause.
func (r *DeviceTokenRepository) ListByUser(ctx context.Context, userID int64, offset, limit int32) ([]sqlc.DeviceToken, error) {
	return r.q.ListDeviceTokensByUser(ctx, sqlc.ListDeviceTokensByUserParams{
		UserID: userID,
		Offset: offset,
		Limit:  limit,
	})
}

// ListByUserIDs returns all active device tokens for a list of user IDs.
//
// This is the hot-path fan-out query used by the notification worker: given a
// segment's resolved user set, it fetches every device the push provider
// should be asked to deliver to. The method deliberately has no pagination —
// the caller supplies the full user list and receives the full token set in
// one round trip, on the assumption that segment sizes are bounded by the
// segment-targeting layer. If userIDs is empty, the query returns an empty
// slice rather than all rows; callers should not rely on the SQL to enforce
// this and should short-circuit on empty input where the cost matters.
func (r *DeviceTokenRepository) ListByUserIDs(ctx context.Context, userIDs []int64) ([]sqlc.DeviceToken, error) {
	return r.q.ListDeviceTokensByUserIDs(ctx, userIDs)
}

// Update applies a partial metadata update to a device token.
//
// Which columns are affected is determined by the sqlc.UpdateDeviceToken
// query and the caller-supplied params; the repository adds no behaviour.
// Unlike UpdateFull, this method is intended for targeted changes such as
// refreshing the last_seen_at timestamp or recording a new app_version
// without rewriting the entire row.
func (r *DeviceTokenRepository) Update(ctx context.Context, params sqlc.UpdateDeviceTokenParams) error {
	return r.q.UpdateDeviceToken(ctx, params)
}

// UpdateStatus toggles the is_active flag on a device token.
//
// It is the mechanism used to soft-disable a token — for example, after the
// push provider reports that the token is no longer registered to a live
// device. The row is not deleted, so historical delivery records that
// reference it remain intact. The method performs an unconditional UPDATE;
// callers do not need to read the current value first.
func (r *DeviceTokenRepository) UpdateStatus(ctx context.Context, params sqlc.UpdateDeviceTokenStatusParams) error {
	return r.q.UpdateDeviceTokenStatus(ctx, params)
}

// Delete removes a device token row by its primary key.
//
// This is a hard delete and is not the recommended way to retire a token
// that the push provider has reported as unregistered — prefer
// UpdateStatus to preserve the historical delivery trail. Use Delete only
// when the caller has determined that the row should no longer exist, such
// as during account deletion or an explicit user-initiated unregistration.
// Deleting a non-existent row is not an error; the underlying query is a
// no-op in that case.
func (r *DeviceTokenRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteDeviceToken(ctx, id)
}

// ExistsByPushToken reports whether a device token with the given push token
// is already present.
//
// It is used by the mobile registration flow to distinguish "first-time
// registration" (insert) from "token refresh" (update) without the cost of
// fetching the full row. The implementation delegates to the
// ExistsDeviceToken SQL query, whose WHERE clause — and in particular whether
// it filters on is_active — determines the exact semantics; consult
// db/queries/device_tokens.sql.
func (r *DeviceTokenRepository) ExistsByPushToken(ctx context.Context, pushToken string) (bool, error) {
	return r.q.ExistsDeviceToken(ctx, pushToken)
}
