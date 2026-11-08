package applications

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/audit"
	"hello/internal/authz"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/httprequest"
	"hello/pkg/response"
)

var (
	errSegmentNameTaken     = errors.New("segment name already exists")
	errSegmentReferenced    = errors.New("segment is still referenced")
	errSegmentMemberExists  = errors.New("segment membership already exists")
	errSegmentMemberMissing = errors.New("segment membership not found")
)

func (s *Service) writeInProject(ctx context.Context, id pgtype.UUID, perm authz.Permission, fn func(context.Context, *sqlc.Queries, sqlc.Application, authz.Principal) error) error {
	if _, err := authz.NewApplicationAuthorizer(s.q).RequireApplication(ctx, id, perm); err != nil {
		return err
	}
	return s.write(ctx, func(ctx context.Context, q *sqlc.Queries) error {
		app, err := repository.NewApplicationRepository(q).LockByPublicID(ctx, id)
		if err != nil {
			return appError(err)
		}
		scope, err := authz.NewApplicationAuthorizer(q).RequireApplication(ctx, id, perm)
		if err != nil {
			return err
		}
		return fn(ctx, q, app, scope.Staff)
	})
}

func lockSegment(ctx context.Context, q *sqlc.Queries, appID, segmentID int64) (sqlc.GetScopedSegmentRow, error) {
	row, err := q.LockScopedSegment(ctx, sqlc.LockScopedSegmentParams{ApplicationID: appID, SegmentID: segmentID})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.GetScopedSegmentRow{}, errSegmentNotFound
	}
	return sqlc.GetScopedSegmentRow(row), err
}

// A zero segment ID denotes creation. Replacements preserve the original creator.
func (s *Service) saveSegment(ctx context.Context, id pgtype.UUID, segmentID int64, name string, description pgtype.Text) (sqlc.GetScopedSegmentRow, error) {
	name, err := cleanName(name)
	if err != nil {
		return sqlc.GetScopedSegmentRow{}, err
	}
	var result sqlc.GetScopedSegmentRow
	err = s.writeInProject(ctx, id, authz.SegmentsWrite, func(ctx context.Context, q *sqlc.Queries, app sqlc.Application, actor authz.Principal) error {
		var before any
		action := audit.ACTION_SEGMENT_CREATE
		if segmentID != 0 {
			row, err := lockSegment(ctx, q, app.ID, segmentID)
			if err != nil {
				return err
			}
			before = segmentDTO(row)
			action = audit.ACTION_SEGMENT_UPDATE
		}
		taken, err := q.ScopedSegmentNameTaken(ctx, sqlc.ScopedSegmentNameTakenParams{ApplicationID: app.ID, Name: name, ExceptID: segmentID})
		if err != nil {
			return err
		}
		if taken {
			return errSegmentNameTaken
		}
		if segmentID == 0 {
			row, err := q.CreateScopedSegment(ctx, sqlc.CreateScopedSegmentParams{ApplicationID: app.ID, Name: name, Description: description, CreatedBy: actor.ID})
			if err != nil {
				return err
			}
			result = sqlc.GetScopedSegmentRow(row)
		} else {
			row, err := q.ReplaceScopedSegment(ctx, sqlc.ReplaceScopedSegmentParams{ApplicationID: app.ID, SegmentID: segmentID, Name: name, Description: description})
			if err != nil {
				return err
			}
			result = sqlc.GetScopedSegmentRow(row)
		}
		return logChange(ctx, q, app, action, audit.ENTITY_TYPE_SEGMENT, result.ID, before, segmentDTO(result))
	})
	if err != nil {
		return sqlc.GetScopedSegmentRow{}, err
	}
	return result, nil
}

func (s *Service) deleteSegment(ctx context.Context, id pgtype.UUID, segmentID int64) error {
	return s.writeInProject(ctx, id, authz.SegmentsWrite, func(ctx context.Context, q *sqlc.Queries, app sqlc.Application, _ authz.Principal) error {
		row, err := lockSegment(ctx, q, app.ID, segmentID)
		if err != nil {
			return err
		}
		referenced, err := q.ScopedSegmentHasReferences(ctx, segmentID)
		if err != nil {
			return err
		}
		if referenced {
			return errSegmentReferenced
		}
		if err := q.DeleteScopedSegment(ctx, sqlc.DeleteScopedSegmentParams{ApplicationID: app.ID, SegmentID: segmentID}); err != nil {
			return err
		}
		return logChange(ctx, q, app, audit.ACTION_SEGMENT_DELETE, audit.ENTITY_TYPE_SEGMENT, segmentID, segmentDTO(row), nil)
	})
}

func (s *Service) changeSegmentMember(ctx context.Context, id pgtype.UUID, segmentID, userID int64, add bool) error {
	return s.writeInProject(ctx, id, authz.MembersWrite, func(ctx context.Context, q *sqlc.Queries, app sqlc.Application, _ authz.Principal) error {
		if _, err := lockSegment(ctx, q, app.ID, segmentID); err != nil {
			return err
		}
		snapshot := struct {
			SegmentID int64 `json:"segment_id"`
			UserID    int64 `json:"user_id"`
		}{segmentID, userID}
		if add {
			memberID, err := q.CreateScopedSegmentMember(ctx, sqlc.CreateScopedSegmentMemberParams{ApplicationID: app.ID, SegmentID: segmentID, UserID: userID})
			if errors.Is(err, pgx.ErrNoRows) {
				return errUserNotFound
			}
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "uq_segment_member" {
				return errSegmentMemberExists
			}
			if err != nil {
				return err
			}
			return logChange(ctx, q, app, audit.ACTION_SEGMENT_MEMBER_ADD, audit.ENTITY_TYPE_SEGMENT_MEMBER, memberID, nil, snapshot)
		}
		memberID, err := q.DeleteScopedSegmentMember(ctx, sqlc.DeleteScopedSegmentMemberParams{ApplicationID: app.ID, SegmentID: segmentID, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errSegmentMemberMissing
		}
		if err != nil {
			return err
		}
		return logChange(ctx, q, app, audit.ACTION_SEGMENT_MEMBER_REMOVE, audit.ENTITY_TYPE_SEGMENT_MEMBER, memberID, snapshot, nil)
	})
}

func (h *Handler) WriteSegment(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err := query(r); err != nil {
		writeError(w, err)
		return
	}
	var segmentID int64
	if r.Method != http.MethodPost {
		segmentID, err = readSegmentID(r)
		if err != nil {
			writeError(w, err)
			return
		}
	}
	if r.Method == http.MethodDelete {
		if !httprequest.RequireEmptyBody(w, r, httprequest.DefaultJSONLimit) {
			return
		}
		if err := h.service.deleteSegment(r.Context(), id, segmentID); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var input struct {
		Name        string  `json:"name"`
		Description *string `json:"description"`
	}
	if !httprequest.DecodeStrictJSONObject(w, r, &input, httprequest.DefaultJSONLimit, "name", "description") {
		return
	}
	var description pgtype.Text
	if input.Description != nil {
		if strings.ContainsRune(*input.Description, 0) {
			badBody(w)
			return
		}
		description = pgtype.Text{String: *input.Description, Valid: true}
	}
	row, err := h.service.saveSegment(r.Context(), id, segmentID, input.Name, description)
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	response.JSON(w, status, segmentDTO(row), "success")
}

func (h *Handler) WriteSegmentMember(w http.ResponseWriter, r *http.Request) {
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
	userID, err := readUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err := query(r); err != nil {
		writeError(w, err)
		return
	}
	if !httprequest.RequireEmptyBody(w, r, httprequest.DefaultJSONLimit) {
		return
	}
	if err := h.service.changeSegmentMember(r.Context(), id, segmentID, userID, r.Method == http.MethodPut); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
