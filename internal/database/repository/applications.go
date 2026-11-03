package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
)

// ApplicationRepository persists provisioning metadata and explicit staff grants.
// It never grants authority: callers authenticate/authorize, validate pagination,
// and own transactions/locks/audit. Bind q to the same transaction for mutations.
// No activation, deletion, identity reassignment or provider work is exposed.
type ApplicationRepository struct{ q sqlc.Querier }

func NewApplicationRepository(q sqlc.Querier) *ApplicationRepository {
	if q == nil {
		panic("applications: queries are required")
	}
	return &ApplicationRepository{q: q}
}
func (r *ApplicationRepository) Create(ctx context.Context, key, name string) (sqlc.Application, error) {
	return r.q.CreateApplication(ctx, sqlc.CreateApplicationParams{Key: key, Name: name})
}
func (r *ApplicationRepository) FindByPublicID(ctx context.Context, id pgtype.UUID) (sqlc.Application, error) {
	return r.q.GetApplicationByPublicID(ctx, id)
}

// LockByPublicID must run in an owning transaction before principal/child locks.
func (r *ApplicationRepository) LockByPublicID(ctx context.Context, id pgtype.UUID) (sqlc.Application, error) {
	return r.q.LockApplicationByPublicID(ctx, id)
}

// Rename returns pgx.ErrNoRows for stale revision or no-op as well as absence.
// The service must distinguish these using the application row it already locked.
func (r *ApplicationRepository) Rename(ctx context.Context, id, revision int64, name string) (sqlc.Application, error) {
	return r.q.RenameApplication(ctx, sqlc.RenameApplicationParams{ID: id, ExpectedRevision: revision, Name: name})
}
func (r *ApplicationRepository) GetAuthority(ctx context.Context, staffID int64, id pgtype.UUID) (sqlc.GetApplicationAuthorityRow, error) {
	return r.q.GetApplicationAuthority(ctx, sqlc.GetApplicationAuthorityParams{StaffID: staffID, PublicID: id})
}
func (r *ApplicationRepository) CreateGrant(ctx context.Context, appID, staffID int64) (sqlc.ApplicationStaffGrant, error) {
	return r.q.CreateApplicationStaffGrant(ctx, sqlc.CreateApplicationStaffGrantParams{ApplicationID: appID, StaffID: staffID})
}
func (r *ApplicationRepository) GetGrant(ctx context.Context, appID, staffID int64) (sqlc.ApplicationStaffGrant, error) {
	return r.q.GetApplicationStaffGrant(ctx, sqlc.GetApplicationStaffGrantParams{ApplicationID: appID, StaffID: staffID})
}

// SetGrantActive is a CAS; absence/stale/no-op produce pgx.ErrNoRows. It does not
// imply HTTP conditional semantics, current target status or an audit commit.
func (r *ApplicationRepository) SetGrantActive(ctx context.Context, appID, staffID, revision int64, active bool) (sqlc.ApplicationStaffGrant, error) {
	return r.q.SetApplicationStaffGrantActive(ctx, sqlc.SetApplicationStaffGrantActiveParams{ApplicationID: appID, StaffID: staffID, ExpectedRevision: revision, IsActive: active})
}

type ApplicationPage struct {
	Total int64
	Items []sqlc.Application
}
type ApplicationGrantPage struct {
	Total int64
	Items []sqlc.ApplicationStaffGrant
}

// Explicit JSON projections belong to the DB page boundary, not HTTP DTOs.
// SQL's single statement returns count plus [] even for an out-of-range page.
type applicationPageRow struct {
	ID        int64              `json:"id"`
	PublicID  pgtype.UUID        `json:"public_id"`
	Key       string             `json:"key"`
	Name      string             `json:"name"`
	Status    string             `json:"status"`
	Revision  int64              `json:"revision"`
	CreatedAt pgtype.Timestamptz `json:"created_at"`
	UpdatedAt pgtype.Timestamptz `json:"updated_at"`
}
type grantPageRow struct {
	ApplicationID int64              `json:"application_id"`
	StaffID       int64              `json:"staff_id"`
	IsActive      bool               `json:"is_active"`
	Revision      int64              `json:"revision"`
	CreatedAt     pgtype.Timestamptz `json:"created_at"`
	UpdatedAt     pgtype.Timestamptz `json:"updated_at"`
}

func decodeApplicationPage(total int64, data []byte) (ApplicationPage, error) {
	var rows []applicationPageRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return ApplicationPage{}, fmt.Errorf("decode application page: %w", err)
	}
	page := ApplicationPage{Total: total, Items: make([]sqlc.Application, 0, len(rows))}
	for _, v := range rows {
		page.Items = append(page.Items, sqlc.Application{ID: v.ID, PublicID: v.PublicID, Key: v.Key, Name: v.Name, Status: v.Status, Revision: v.Revision, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt})
	}
	return page, nil
}
func (r *ApplicationRepository) ListManaged(ctx context.Context, key string, offset, limit int32) (ApplicationPage, error) {
	row, err := r.q.ListManagedApplications(ctx, sqlc.ListManagedApplicationsParams{FilterKey: key, OffsetCount: offset, LimitCount: limit})
	if err != nil {
		return ApplicationPage{}, err
	}
	return decodeApplicationPage(row.Total, row.Items)
}

// ListGranted filters active membership only; this is not caller authentication.
func (r *ApplicationRepository) ListGranted(ctx context.Context, staffID int64, offset, limit int32) (ApplicationPage, error) {
	row, err := r.q.ListGrantedApplications(ctx, sqlc.ListGrantedApplicationsParams{StaffID: staffID, OffsetCount: offset, LimitCount: limit})
	if err != nil {
		return ApplicationPage{}, err
	}
	return decodeApplicationPage(row.Total, row.Items)
}
func (r *ApplicationRepository) ListGrants(ctx context.Context, appID int64, offset, limit int32) (ApplicationGrantPage, error) {
	row, err := r.q.ListApplicationStaffGrants(ctx, sqlc.ListApplicationStaffGrantsParams{ApplicationID: appID, OffsetCount: offset, LimitCount: limit})
	if err != nil {
		return ApplicationGrantPage{}, err
	}
	var rows []grantPageRow
	if err := json.Unmarshal(row.Items, &rows); err != nil {
		return ApplicationGrantPage{}, fmt.Errorf("decode application grants: %w", err)
	}
	page := ApplicationGrantPage{Total: row.Total, Items: make([]sqlc.ApplicationStaffGrant, 0, len(rows))}
	for _, v := range rows {
		page.Items = append(page.Items, sqlc.ApplicationStaffGrant{ApplicationID: v.ApplicationID, StaffID: v.StaffID, IsActive: v.IsActive, Revision: v.Revision, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt})
	}
	return page, nil
}
