package applications

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/audit"
	"hello/internal/authz"
	"hello/internal/database/sqlc"
	"hello/internal/httprequest"
	"hello/pkg/response"
)

var (
	errTemplateNotFound  = errors.New("template not found")
	errInvalidTemplateID = errors.New("invalid template id")
)

type templateInput struct {
	Name   string `json:"name"`
	Title  string `json:"title_template"`
	Body   string `json:"body_template"`
	Active *bool  `json:"is_active"`
}

type templateDTO struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Title  string `json:"title_template"`
	Body   string `json:"body_template"`
	Active bool   `json:"is_active"`
}

func templateView(row sqlc.GetScopedTemplateRow) templateDTO {
	return templateDTO{strconv.FormatInt(row.ID, 10), row.Name, row.TitleTemplate, row.BodyTemplate, row.IsActive}
}

func templateLookupError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errTemplateNotFound
	}
	return err
}

func (s *Service) listTemplates(ctx context.Context, id pgtype.UUID, page userReadPage) ([]sqlc.ListScopedTemplatesRow, error) {
	scope, err := authz.NewApplicationAuthorizer(s.q).RequireApplication(ctx, id, authz.NotificationsRead)
	if err != nil {
		return nil, err
	}
	return s.q.ListScopedTemplates(ctx, sqlc.ListScopedTemplatesParams{ApplicationID: scope.ApplicationID, AfterID: page.after, PageSize: page.limit + 1})
}

func (s *Service) getTemplate(ctx context.Context, id pgtype.UUID, templateID int64) (sqlc.GetScopedTemplateRow, error) {
	scope, err := authz.NewApplicationAuthorizer(s.q).RequireApplication(ctx, id, authz.NotificationsRead)
	if err != nil {
		return sqlc.GetScopedTemplateRow{}, err
	}
	row, err := s.q.GetScopedTemplate(ctx, sqlc.GetScopedTemplateParams{ApplicationID: scope.ApplicationID, TemplateID: templateID})
	return row, templateLookupError(err)
}

func (s *Service) saveTemplate(ctx context.Context, id pgtype.UUID, templateID int64, input templateInput) (sqlc.GetScopedTemplateRow, error) {
	var result sqlc.GetScopedTemplateRow
	err := s.writeInProject(ctx, id, authz.NotificationsWrite, func(q *sqlc.Queries, app sqlc.Application, actor authz.Principal) error {
		action := audit.ACTION_TEMPLATE_CREATE
		if templateID == 0 {
			row, err := q.CreateScopedTemplate(ctx, sqlc.CreateScopedTemplateParams{ApplicationID: app.ID, Name: input.Name, TitleTemplate: input.Title, BodyTemplate: input.Body, IsActive: *input.Active, CreatedBy: actor.ID})
			if err != nil {
				return err
			}
			result = sqlc.GetScopedTemplateRow(row)
		} else {
			action = audit.ACTION_TEMPLATE_UPDATE
			row, err := q.ReplaceScopedTemplate(ctx, sqlc.ReplaceScopedTemplateParams{ApplicationID: app.ID, TemplateID: templateID, Name: input.Name, TitleTemplate: input.Title, BodyTemplate: input.Body, IsActive: *input.Active})
			if err != nil {
				return templateLookupError(err)
			}
			result = sqlc.GetScopedTemplateRow(row)
		}
		snapshot := struct {
			Name   string `json:"name"`
			Active bool   `json:"is_active"`
		}{result.Name, result.IsActive}
		return logChange(ctx, q, app, action, audit.ENTITY_TYPE_TEMPLATE, result.ID, nil, snapshot)
	})
	return result, err
}

func readTemplateID(r *http.Request) (int64, error) {
	value, ok := campaignReferenceID(r.PathValue("template_id"))
	if !ok {
		return 0, errInvalidTemplateID
	}
	return value.Int64, nil
}

func (h *Handler) ListTemplates(w http.ResponseWriter, r *http.Request) {
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
	rows, err := h.service.listTemplates(r.Context(), id, page)
	if err != nil {
		writeError(w, err)
		return
	}
	result := makeReadPage(rows, page.limit, func(row sqlc.ListScopedTemplatesRow) (templateDTO, string) {
		item := templateView(sqlc.GetScopedTemplateRow(row))
		return item, item.ID
	})
	response.JSON(w, http.StatusOK, result, "success")
}

func (h *Handler) GetTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	templateID, err := readTemplateID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err := query(r); err != nil {
		writeError(w, err)
		return
	}
	row, err := h.service.getTemplate(r.Context(), id, templateID)
	if err != nil {
		writeError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, templateView(row), "success")
}

func (h *Handler) WriteTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err := query(r); err != nil {
		writeError(w, err)
		return
	}
	var templateID int64
	status := http.StatusCreated
	if r.Method == http.MethodPut {
		templateID, err = readTemplateID(r)
		if err != nil {
			writeError(w, err)
			return
		}
		status = http.StatusOK
	}
	var input templateInput
	if !httprequest.DecodeStrictJSONObject(w, r, &input, httprequest.DefaultJSONLimit, "name", "title_template", "body_template", "is_active") {
		return
	}
	input.Name, err = cleanName(input.Name)
	if err != nil || input.Active == nil || strings.TrimSpace(input.Title) == "" || strings.TrimSpace(input.Body) == "" ||
		len(input.Title) > 255 || len(input.Body) > 4096 || strings.ContainsRune(input.Title, 0) || strings.ContainsRune(input.Body, 0) {
		badBody(w)
		return
	}
	row, err := h.service.saveTemplate(r.Context(), id, templateID, input)
	if err != nil {
		writeError(w, err)
		return
	}
	response.JSON(w, status, templateView(row), "success")
}
