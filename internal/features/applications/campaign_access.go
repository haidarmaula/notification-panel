package applications

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/audit"
	"hello/internal/authz"
	"hello/internal/database/sqlc"
	"hello/internal/httprequest"
	"hello/pkg/response"
)

var (
	errCampaignNotFound  = errors.New("campaign not found")
	errCampaignNotDraft  = errors.New("campaign is not a draft")
	errCampaignProtected = errors.New("campaign has protected records")
	errInvalidCampaignID = errors.New("invalid campaign id")
)

type campaignSummary struct {
	campaignDraftResult
	Title string `json:"title"`
}

func campaignSummaryFor(id int64, status, title string) campaignSummary {
	return campaignSummary{campaignDraftResult{ID: strconv.FormatInt(id, 10), Status: status}, title}
}

func campaignLookupError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errCampaignNotFound
	}
	return err
}

func (s *Service) listCampaigns(ctx context.Context, id pgtype.UUID, page userReadPage) ([]sqlc.ListScopedCampaignsRow, error) {
	scope, err := authz.NewApplicationAuthorizer(s.q).RequireApplication(ctx, id, authz.NotificationsRead)
	if err != nil {
		return nil, err
	}
	return s.q.ListScopedCampaigns(ctx, sqlc.ListScopedCampaignsParams{ApplicationID: scope.ApplicationID, AfterID: page.after, PageSize: page.limit + 1})
}

func (s *Service) getCampaign(ctx context.Context, id pgtype.UUID, campaignID int64) (sqlc.GetScopedCampaignRow, error) {
	scope, err := authz.NewApplicationAuthorizer(s.q).RequireApplication(ctx, id, authz.NotificationsRead)
	if err != nil {
		return sqlc.GetScopedCampaignRow{}, err
	}
	row, err := s.q.GetScopedCampaign(ctx, sqlc.GetScopedCampaignParams{ApplicationID: scope.ApplicationID, CampaignID: campaignID})
	return row, campaignLookupError(err)
}

func (s *Service) mutateCampaign(ctx context.Context, id pgtype.UUID, campaignID int64, input *campaignDraftInput, templateID pgtype.Int8, targets []sqlc.CreateScopedCampaignTargetParams) error {
	return s.writeInProject(ctx, id, authz.NotificationsWrite, func(ctx context.Context, q *sqlc.Queries, app sqlc.Application, _ authz.Principal) error {
		status, err := q.LockScopedCampaign(ctx, sqlc.LockScopedCampaignParams{ApplicationID: app.ID, CampaignID: campaignID})
		if err != nil {
			return campaignLookupError(err)
		}
		if status != "DRAFT" {
			return errCampaignNotDraft
		}
		protected, err := q.CampaignHasProtectedChildren(ctx, sqlc.CampaignHasProtectedChildrenParams{ApplicationID: app.ID, CampaignID: campaignID})
		if err != nil {
			return err
		}
		if protected {
			return errCampaignProtected
		}
		snapshot := campaignDraftResult{ID: strconv.FormatInt(campaignID, 10), Status: status}
		if input == nil {
			n, err := q.DeleteScopedCampaignDraft(ctx, sqlc.DeleteScopedCampaignDraftParams{ApplicationID: app.ID, CampaignID: campaignID})
			if err != nil {
				return err
			}
			if n != 1 {
				return errCampaignNotDraft
			}
			return logChange(ctx, q, app, audit.ACTION_NOTIFICATION_DELETE, audit.ENTITY_TYPE_NOTIFICATION, campaignID, snapshot, nil)
		}
		name, err := scopedCampaignTemplate(ctx, q, app.ID, templateID)
		if err != nil {
			return err
		}
		n, err := q.ReplaceScopedCampaignDraft(ctx, sqlc.ReplaceScopedCampaignDraftParams{ApplicationID: app.ID, CampaignID: campaignID, Title: input.Title, Body: input.Body, TemplateID: templateID, TemplateName: name})
		if err != nil {
			return err
		}
		if n != 1 {
			return errCampaignNotDraft
		}
		if err := q.ClearScopedCampaignTargets(ctx, sqlc.ClearScopedCampaignTargetsParams{ApplicationID: app.ID, CampaignID: campaignID}); err != nil {
			return err
		}
		if err := insertCampaignTargets(ctx, q, app.ID, campaignID, targets); err != nil {
			return err
		}
		return logChange(ctx, q, app, audit.ACTION_NOTIFICATION_UPDATE, audit.ENTITY_TYPE_NOTIFICATION, campaignID, nil, struct {
			campaignDraftResult
			TargetCount int `json:"target_count"`
		}{snapshot, len(targets)})
	})
}

func campaignPathID(r *http.Request) (pgtype.UUID, int64, error) {
	id, err := pathID(r)
	if err != nil {
		return id, 0, err
	}
	campaign, ok := campaignReferenceID(r.PathValue("campaign_id"))
	if !ok {
		return id, 0, errInvalidCampaignID
	}
	return id, campaign.Int64, nil
}
func campaignPath(r *http.Request) (pgtype.UUID, int64, error) {
	id, campaignID, err := campaignPathID(r)
	if err != nil {
		return id, 0, err
	}
	_, err = query(r)
	return id, campaignID, err
}

func (h *Handler) ListCampaigns(w http.ResponseWriter, r *http.Request) {
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
	rows, err := h.service.listCampaigns(r.Context(), id, page)
	if err != nil {
		writeError(w, err)
		return
	}
	result := makeReadPage(rows, page.limit, func(row sqlc.ListScopedCampaignsRow) (campaignSummary, string) {
		item := campaignSummaryFor(row.ID, row.Status, row.Title)
		return item, item.ID
	})
	response.JSON(w, http.StatusOK, result, "success")
}

func (h *Handler) GetCampaign(w http.ResponseWriter, r *http.Request) {
	id, campaignID, err := campaignPath(r)
	if err != nil {
		writeError(w, err)
		return
	}
	row, err := h.service.getCampaign(r.Context(), id, campaignID)
	if err != nil {
		writeError(w, err)
		return
	}
	var templateID *string
	if row.TemplateID.Valid {
		value := strconv.FormatInt(row.TemplateID.Int64, 10)
		templateID = &value
	}
	response.JSON(w, http.StatusOK, struct {
		campaignSummary
		Body         string          `json:"body"`
		TemplateID   *string         `json:"template_id"`
		TemplateName pgtype.Text     `json:"template_name"`
		Targets      json.RawMessage `json:"targets"`
	}{campaignSummaryFor(row.ID, row.Status, row.Title), row.Body, templateID, row.TemplateName, row.Targets}, "success")
}

func (h *Handler) MutateCampaign(w http.ResponseWriter, r *http.Request) {
	id, campaignID, err := campaignPath(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if r.Method == http.MethodDelete {
		if !httprequest.RequireEmptyBody(w, r, httprequest.DefaultJSONLimit) {
			return
		}
		if err := h.service.mutateCampaign(r.Context(), id, campaignID, nil, pgtype.Int8{}, nil); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
	if err := h.service.mutateCampaign(r.Context(), id, campaignID, &input, templateID, targets); err != nil {
		writeError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, campaignDraftResult{ID: strconv.FormatInt(campaignID, 10), Status: "DRAFT"}, "success")
}
