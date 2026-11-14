package authz_test

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

type authority struct {
	role string
	err  error
}

func (a authority) GetStaffAuthority(ctx context.Context, id int64) (sqlc.GetStaffAuthorityRow, error) {
	return sqlc.GetStaffAuthorityRow{ID: id, IsActive: true, RoleName: pgtype.Text{String: a.role, Valid: true}}, a.err
}

func serviceIdentity() context.Context {
	return context.WithValue(context.Background(), middleware.UserContextKey, &token.AccessClaims{StaffID: 7, RoleName: "SUPER_ADMIN"})
}
