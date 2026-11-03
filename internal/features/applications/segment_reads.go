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
	errInvalidSegmentID = errors.New("invalid segment id")
	errSegmentNotFound  = errors.New("segment not found")
)

type scopedSegmentDTO struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description pgtype.Text        `json:"description"`
	CreatedAt   pgtype.Timestamptz `json:"created_at"`
	UpdatedAt   pgtype.Timestamptz `json:"updated_at"`
}

type scopedMemberDTO struct {
	ID         string             `json:"id"`
	UserID     string             `json:"user_id"`
	ExternalID string             `json:"external_id"`
	Name       pgtype.Text        `json:"name"`
	CreatedAt  pgtype.Timestamptz `json:"created_at"`
}

func segmentDTO(row sqlc.GetScopedSegmentRow) scopedSegmentDTO {
	return scopedSegmentDTO{strconv.FormatInt(row.ID, 10), row.Name, row.Description, row.CreatedAt, row.UpdatedAt}
}

func (s *Service) requireSegmentRead(ctx context.Context, id pgtype.UUID, permission authz.Permission) (authz.ApplicationPrincipal, error) {
	return authz.NewApplicationAuthorizer(s.q).RequireApplication(ctx, id, permission)
}

func (s *Service) scopedSegment(ctx context.Context, appID, segmentID int64) (sqlc.GetScopedSegmentRow, error) {
	if segmentID <= 0 {
		return sqlc.GetScopedSegmentRow{}, errInvalidSegmentID
	}
	row, err := s.q.GetScopedSegment(ctx, sqlc.GetScopedSegmentParams{ApplicationID: appID, SegmentID: segmentID})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, errSegmentNotFound
	}
	return row, err
}

func (s *Service) getSegment(ctx context.Context, id pgtype.UUID, segmentID int64) (sqlc.GetScopedSegmentRow, error) {
	scope, err := s.requireSegmentRead(ctx, id, authz.SegmentsRead)
	if err != nil {
		return sqlc.GetScopedSegmentRow{}, err
	}
	return s.scopedSegment(ctx, scope.ApplicationID, segmentID)
}

func (s *Service) listSegments(ctx context.Context, id pgtype.UUID, page userReadPage) ([]sqlc.ListScopedSegmentsRow, error) {
	scope, err := s.requireSegmentRead(ctx, id, authz.SegmentsRead)
	if err != nil {
		return nil, err
	}
	if !page.valid() {
		return nil, ErrInvalidPagination
	}
	return s.q.ListScopedSegments(ctx, sqlc.ListScopedSegmentsParams{ApplicationID: scope.ApplicationID, AfterID: page.after, PageSize: page.limit + 1})
}

func (s *Service) listSegmentMembers(ctx context.Context, id pgtype.UUID, segmentID int64, page userReadPage) ([]sqlc.ListScopedSegmentMembersRow, error) {
	scope, err := s.requireSegmentRead(ctx, id, authz.MembersRead)
	if err != nil {
		return nil, err
	}
	if !page.valid() {
		return nil, ErrInvalidPagination
	}
	if _, err := s.scopedSegment(ctx, scope.ApplicationID, segmentID); err != nil {
		return nil, err
	}
	return s.q.ListScopedSegmentMembers(ctx, sqlc.ListScopedSegmentMembersParams{ApplicationID: scope.ApplicationID, SegmentID: segmentID, AfterID: page.after, PageSize: page.limit + 1})
}

func readSegmentID(r *http.Request) (int64, error) {
	value := r.PathValue("segment_id")
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || !staffIDPattern.MatchString(value) {
		return 0, errInvalidSegmentID
	}
	return id, nil
}

func (h *Handler) ListSegments(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	page, err := readPagination(r)
	if err != nil {
		writeError(w, err)
		return
	}
	rows, err := h.service.listSegments(r.Context(), id, page)
	if err != nil {
		writeError(w, err)
		return
	}
	result := makeReadPage(rows, page.limit, func(row sqlc.ListScopedSegmentsRow) (scopedSegmentDTO, string) {
		item := segmentDTO(sqlc.GetScopedSegmentRow(row))
		return item, item.ID
	})
	response.JSON(w, http.StatusOK, result, "success")
}

func (h *Handler) GetSegment(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	segmentID, err := readSegmentID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err := query(r); err != nil {
		writeError(w, err)
		return
	}
	row, err := h.service.getSegment(r.Context(), id, segmentID)
	if err != nil {
		writeError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, segmentDTO(row), "success")
}

func (h *Handler) ListSegmentMembers(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	segmentID, err := readSegmentID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	page, err := readPagination(r)
	if err != nil {
		writeError(w, err)
		return
	}
	rows, err := h.service.listSegmentMembers(r.Context(), id, segmentID, page)
	if err != nil {
		writeError(w, err)
		return
	}
	result := makeReadPage(rows, page.limit, func(row sqlc.ListScopedSegmentMembersRow) (scopedMemberDTO, string) {
		id := strconv.FormatInt(row.ID, 10)
		return scopedMemberDTO{id, strconv.FormatInt(row.UserID, 10), row.ExternalID, row.Name, row.CreatedAt}, id
	})
	response.JSON(w, http.StatusOK, result, "success")
}
