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

var errCampaignReference = errors.New("campaign reference not found")

type campaignTargetInput struct {
	Type string  `json:"type"`
	ID   *string `json:"id,omitempty"`
}

type campaignDraftInput struct {
	Title      string                `json:"title"`
	Body       string                `json:"body"`
	TemplateID *string               `json:"template_id,omitempty"`
	Targets    []campaignTargetInput `json:"targets"`
}

type campaignDraftResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func campaignReferenceID(value string) (pgtype.Int8, bool) {
	id, err := strconv.ParseInt(value, 10, 64)
	return pgtype.Int8{Int64: id, Valid: true}, err == nil && staffIDPattern.MatchString(value)
}

func (in campaignDraftInput) validate() (pgtype.Int8, []sqlc.CreateScopedCampaignTargetParams, bool) {
	if strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Body) == "" ||
		len(in.Title) > 255 || len(in.Body) > 4096 || strings.ContainsRune(in.Title, 0) || strings.ContainsRune(in.Body, 0) ||
		len(in.Targets) < 1 || len(in.Targets) > 100 {
		return pgtype.Int8{}, nil, false
	}
	var templateID pgtype.Int8
	if in.TemplateID != nil {
		var ok bool
		templateID, ok = campaignReferenceID(*in.TemplateID)
		if !ok {
			return pgtype.Int8{}, nil, false
		}
	}
	targets := make([]sqlc.CreateScopedCampaignTargetParams, 0, len(in.Targets))
	seen := make(map[sqlc.CreateScopedCampaignTargetParams]struct{}, len(in.Targets))
	for _, input := range in.Targets {
		target := sqlc.CreateScopedCampaignTargetParams{TargetType: input.Type}
		switch input.Type {
		case "BROADCAST":
			if input.ID != nil || len(in.Targets) != 1 {
				return pgtype.Int8{}, nil, false
			}
		case "SEGMENT", "INDIVIDUAL":
			if input.ID == nil {
				return pgtype.Int8{}, nil, false
			}
			id, ok := campaignReferenceID(*input.ID)
			if !ok {
				return pgtype.Int8{}, nil, false
			}
			if input.Type == "SEGMENT" {
				target.SegmentID = id
			} else {
				target.UserID = id
			}
		default:
			return pgtype.Int8{}, nil, false
		}
		if _, duplicate := seen[target]; duplicate {
			return pgtype.Int8{}, nil, false
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}
	return templateID, targets, true
}

func (s *Service) createCampaignDraft(ctx context.Context, id pgtype.UUID, input campaignDraftInput, templateID pgtype.Int8, targets []sqlc.CreateScopedCampaignTargetParams) (campaignDraftResult, error) {
	var result campaignDraftResult
	err := s.writeInProject(ctx, id, authz.NotificationsWrite, func(q *sqlc.Queries, app sqlc.Application, actor authz.Principal) error {
		var templateName pgtype.Text
		if templateID.Valid {
			name, err := q.LockScopedCampaignTemplate(ctx, sqlc.LockScopedCampaignTemplateParams{ApplicationID: app.ID, TemplateID: templateID.Int64})
			if err != nil {
				return campaignReferenceError(err)
			}
			templateName = pgtype.Text{String: name, Valid: true}
		}
		draftID, err := q.CreateScopedCampaignDraft(ctx, sqlc.CreateScopedCampaignDraftParams{ApplicationID: app.ID, TemplateID: templateID, TemplateName: templateName, Title: input.Title, Body: input.Body, CreatedBy: actor.ID})
		if err != nil {
			return err
		}
		for _, target := range targets {
			target.ApplicationID = app.ID
			target.NotificationID = draftID
			if _, err := q.CreateScopedCampaignTarget(ctx, target); err != nil {
				return campaignReferenceError(err)
			}
		}
		result = campaignDraftResult{ID: strconv.FormatInt(draftID, 10), Status: "DRAFT"}
		snapshot := struct {
			campaignDraftResult
			TargetCount int `json:"target_count"`
		}{result, len(targets)}
		return logChange(ctx, q, app, audit.ACTION_NOTIFICATION_CREATE, audit.ENTITY_TYPE_NOTIFICATION, draftID, nil, snapshot)
	})
	if err != nil {
		return campaignDraftResult{}, err
	}
	return result, nil
}

func campaignReferenceError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errCampaignReference
	}
	return err
}

func (h *Handler) CreateCampaignDraft(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err := query(r); err != nil {
		writeError(w, err)
		return
	}
	var input campaignDraftInput
	if !httprequest.DecodeStrictJSONObject(w, r, &input, httprequest.DefaultJSONLimit, "title", "body", "template_id", "targets") {
		return
	}
	templateID, targets, ok := input.validate()
	if !ok {
		badBody(w)
		return
	}
	draft, err := h.service.createCampaignDraft(r.Context(), id, input, templateID, targets)
	if err != nil {
		writeError(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, draft, "success")
}
