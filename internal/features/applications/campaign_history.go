package applications

import (
	"context"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/authz"
	"hello/internal/database/sqlc"
	"hello/pkg/response"
)

type deliveryDTO struct {
	ID             string             `json:"id"`
	UserID         string             `json:"user_id"`
	InstallationID string             `json:"installation_id"`
	Status         string             `json:"status"`
	SentAt         pgtype.Timestamptz `json:"sent_at"`
	DeliveredAt    pgtype.Timestamptz `json:"delivered_at"`
	OpenedAt       pgtype.Timestamptz `json:"opened_at"`
}

type campaignReadDTO struct {
	ID     string             `json:"id"`
	UserID string             `json:"user_id"`
	ReadAt pgtype.Timestamptz `json:"read_at"`
}

func (s *Service) campaignHistoryScope(ctx context.Context, id pgtype.UUID, campaignID int64) (int64, error) {
	scope, err := authz.NewApplicationAuthorizer(s.q).RequireApplication(ctx, id, authz.NotificationsRead)
	if err != nil {
		return 0, err
	}
	_, err = s.q.GetScopedCampaignID(ctx, sqlc.GetScopedCampaignIDParams{ApplicationID: scope.ApplicationID, CampaignID: campaignID})
	return scope.ApplicationID, campaignLookupError(err)
}

func (s *Service) listCampaignDeliveries(ctx context.Context, id pgtype.UUID, campaignID int64, page deliveryReadPage) ([]sqlc.ListScopedCampaignDeliveriesRow, error) {
	appID, err := s.campaignHistoryScope(ctx, id, campaignID)
	if err != nil {
		return nil, err
	}
	return s.q.ListScopedCampaignDeliveries(ctx, sqlc.ListScopedCampaignDeliveriesParams{ApplicationID: appID, CampaignID: campaignID, AfterID: page.after, PageSize: page.limit + 1})
}

func (s *Service) listCampaignReads(ctx context.Context, id pgtype.UUID, campaignID int64, page userReadPage) ([]sqlc.ListScopedCampaignReadsRow, error) {
	appID, err := s.campaignHistoryScope(ctx, id, campaignID)
	if err != nil {
		return nil, err
	}
	return s.q.ListScopedCampaignReads(ctx, sqlc.ListScopedCampaignReadsParams{ApplicationID: appID, CampaignID: campaignID, AfterID: page.after, PageSize: page.limit + 1})
}

type deliveryReadPage struct {
	after pgtype.UUID
	limit int32
}

// UUID order is not chronological; an omitted cursor starts at zero.
func deliveryPagination(r *http.Request) (deliveryReadPage, error) {
	v, err := query(r, "after_id", "limit")
	p := deliveryReadPage{after: pgtype.UUID{Valid: true}, limit: 20}
	if err != nil {
		return p, err
	}
	if v.Has("after_id") {
		value := v.Get("after_id")
		if !uuidPattern.MatchString(value) || p.after.Scan(value) != nil || p.after.Bytes == [16]byte{} || p.after.String() != value {
			return p, ErrInvalidPagination
		}
	}
	if v.Has("limit") {
		value := v.Get("limit")
		n, err := strconv.ParseInt(value, 10, 32)
		if err != nil || !staffIDPattern.MatchString(value) || n < 1 || n > 100 {
			return p, ErrInvalidPagination
		}
		p.limit = int32(n)
	}
	return p, nil
}

func (h *Handler) ListCampaignDeliveries(w http.ResponseWriter, r *http.Request) {
	id, campaignID, err := campaignPathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	page, err := deliveryPagination(r)
	if err != nil {
		writeError(w, err)
		return
	}
	rows, err := h.service.listCampaignDeliveries(r.Context(), id, campaignID, page)
	if err != nil {
		writeError(w, err)
		return
	}
	result := makeReadPage(rows, page.limit, func(row sqlc.ListScopedCampaignDeliveriesRow) (deliveryDTO, string) {
		item := deliveryDTO{
			ID: row.ID.String(), UserID: strconv.FormatInt(row.UserID, 10),
			InstallationID: row.InstallationID.String(), Status: row.Status,
			SentAt: row.SentAt, DeliveredAt: row.DeliveredAt, OpenedAt: row.OpenedAt,
		}
		return item, item.ID
	})
	response.JSON(w, http.StatusOK, result, "success")
}

func (h *Handler) ListCampaignReads(w http.ResponseWriter, r *http.Request) {
	id, campaignID, err := campaignPathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	page, err := readPagination(r)
	if err != nil {
		writeError(w, err)
		return
	}
	rows, err := h.service.listCampaignReads(r.Context(), id, campaignID, page)
	if err != nil {
		writeError(w, err)
		return
	}
	result := makeReadPage(rows, page.limit, func(row sqlc.ListScopedCampaignReadsRow) (campaignReadDTO, string) {
		item := campaignReadDTO{strconv.FormatInt(row.ID, 10), strconv.FormatInt(row.UserID, 10), row.ReadAt}
		return item, item.ID
	})
	response.JSON(w, http.StatusOK, result, "success")
}
