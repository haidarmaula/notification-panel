package audit

import (
	"context"
	"encoding/json"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

type AuditService struct {
	repo *repository.AuditLogRepository
}

func NewAuditService(repo *repository.AuditLogRepository) *AuditService {
	return &AuditService{
		repo: repo,
	}
}

func (s *AuditService) Log(ctx context.Context, params LogParams) error {
	var beforeJSON, afterJSON []byte
	var err error

	if params.Before != nil {
		beforeJSON, err = json.Marshal(params.Before)
		if err != nil {
			return err
		}
	}

	if params.After != nil {
		afterJSON, err = json.Marshal(params.After)
		if err != nil {
			return err
		}
	}

	staffID, ipAddress, userAgent, _ := GetAuditContext(ctx)
	_, err = s.repo.Create(ctx, sqlc.CreateAuditLogParams{
		ActorUserID: staffID,
		Action:      params.Action,
		EntityType:  params.EntityType,
		EntityName:  pgtype.Text{String: params.EntityName, Valid: params.EntityName != ""},
		EntityID:    pgtype.Int8{Int64: params.EntityID, Valid: params.EntityID != 0},
		BeforeJson:  beforeJSON,
		AfterJson:   afterJSON,
		IpAddress:   pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
		UserAgent:   pgtype.Text{String: userAgent, Valid: userAgent != ""},
	})

	return err
}
