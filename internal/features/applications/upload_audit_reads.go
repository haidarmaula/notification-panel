package applications

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/authz"
	"hello/internal/database/sqlc"
	"hello/pkg/response"
)

var (
	errInvalidBatchID      = errors.New("invalid batch id")
	errUploadBatchNotFound = errors.New("upload batch not found")
)

type uploadBatchDTO struct {
	ID          string             `json:"id"`
	TotalRows   int32              `json:"total_rows"`
	ValidRows   int32              `json:"valid_rows"`
	InvalidRows int32              `json:"invalid_rows"`
	CreatedAt   pgtype.Timestamptz `json:"created_at"`
}

type uploadRowDTO struct {
	ID        string             `json:"id"`
	IsValid   bool               `json:"is_valid"`
	CreatedAt pgtype.Timestamptz `json:"created_at"`
}

type projectAuditDTO struct {
	ID         string             `json:"id"`
	ActorID    string             `json:"actor_id"`
	Action     string             `json:"action"`
	EntityType string             `json:"entity_type"`
	EntityID   *string            `json:"entity_id"`
	CreatedAt  pgtype.Timestamptz `json:"created_at"`
}

func (s *Service) listUploadBatches(ctx context.Context, id pgtype.UUID, page userReadPage) ([]sqlc.ListScopedUploadBatchesRow, error) {
	scope, err := authz.NewApplicationAuthorizer(s.q).RequireApplication(ctx, id, authz.NotificationsRead)
	if err != nil {
		return nil, err
	}
	return s.q.ListScopedUploadBatches(ctx, sqlc.ListScopedUploadBatchesParams{ApplicationID: scope.ApplicationID, AfterID: page.after, PageSize: page.limit + 1})
}

func (s *Service) listUploadRows(ctx context.Context, id pgtype.UUID, batchID int64, page userReadPage) ([]sqlc.ListScopedUploadRowsRow, error) {
	scope, err := authz.NewApplicationAuthorizer(s.q).RequireApplication(ctx, id, authz.NotificationsRead)
	if err != nil {
		return nil, err
	}
	_, err = s.q.GetScopedUploadBatchID(ctx, sqlc.GetScopedUploadBatchIDParams{ApplicationID: scope.ApplicationID, BatchID: batchID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errUploadBatchNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.q.ListScopedUploadRows(ctx, sqlc.ListScopedUploadRowsParams{ApplicationID: scope.ApplicationID, BatchID: batchID, AfterID: page.after, PageSize: page.limit + 1})
}

func (s *Service) listProjectAudit(ctx context.Context, id pgtype.UUID, page userReadPage) ([]sqlc.ListScopedAuditLogsRow, error) {
	scope, err := authz.NewApplicationAuthorizer(s.q).RequireApplication(ctx, id, authz.AuditRead)
	if err != nil {
		return nil, err
	}
	return s.q.ListScopedAuditLogs(ctx, sqlc.ListScopedAuditLogsParams{ApplicationID: scope.ApplicationID, AfterID: page.after, PageSize: page.limit + 1})
}

func projectReadPage(r *http.Request) (pgtype.UUID, userReadPage, error) {
	id, err := pathID(r)
	if err != nil {
		return id, userReadPage{}, err
	}
	page, err := readPagination(r)
	return id, page, err
}

func (h *Handler) ListUploadBatches(w http.ResponseWriter, r *http.Request) {
	id, page, err := projectReadPage(r)
	if err != nil {
		writeError(w, err)
		return
	}
	rows, err := h.service.listUploadBatches(r.Context(), id, page)
	if err != nil {
		writeError(w, err)
		return
	}
	result := makeReadPage(rows, page.limit, func(row sqlc.ListScopedUploadBatchesRow) (uploadBatchDTO, string) {
		item := uploadBatchDTO{strconv.FormatInt(row.ID, 10), row.TotalRows, row.ValidRows, row.InvalidRows, row.CreatedAt}
		return item, item.ID
	})
	response.JSON(w, http.StatusOK, result, "success")
}

func (h *Handler) ListUploadRows(w http.ResponseWriter, r *http.Request) {
	id, page, err := projectReadPage(r)
	if err != nil {
		writeError(w, err)
		return
	}
	batch, ok := campaignReferenceID(r.PathValue("batch_id"))
	if !ok {
		writeError(w, errInvalidBatchID)
		return
	}
	rows, err := h.service.listUploadRows(r.Context(), id, batch.Int64, page)
	if err != nil {
		writeError(w, err)
		return
	}
	result := makeReadPage(rows, page.limit, func(row sqlc.ListScopedUploadRowsRow) (uploadRowDTO, string) {
		item := uploadRowDTO{strconv.FormatInt(row.ID, 10), row.IsValid, row.CreatedAt}
		return item, item.ID
	})
	response.JSON(w, http.StatusOK, result, "success")
}

func (h *Handler) ListProjectAudit(w http.ResponseWriter, r *http.Request) {
	id, page, err := projectReadPage(r)
	if err != nil {
		writeError(w, err)
		return
	}
	rows, err := h.service.listProjectAudit(r.Context(), id, page)
	if err != nil {
		writeError(w, err)
		return
	}
	result := makeReadPage(rows, page.limit, func(row sqlc.ListScopedAuditLogsRow) (projectAuditDTO, string) {
		var entityID *string
		if row.EntityID.Valid {
			value := strconv.FormatInt(row.EntityID.Int64, 10)
			entityID = &value
		}
		item := projectAuditDTO{strconv.FormatInt(row.ID, 10), strconv.FormatInt(row.ActorUserID, 10), row.Action, row.EntityType, entityID, row.CreatedAt}
		return item, item.ID
	})
	response.JSON(w, http.StatusOK, result, "success")
}
