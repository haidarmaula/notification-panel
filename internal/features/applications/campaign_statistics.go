package applications

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/authz"
	"hello/internal/database/sqlc"
	"hello/pkg/response"
)

type campaignCounts struct {
	TargetedUsers           int64 `json:"targeted_users"`
	EligibleUsers           int64 `json:"eligible_users"`
	TargetedDevices         int64 `json:"targeted_devices"`
	PendingDevices          int64 `json:"pending_devices"`
	ProviderAcceptedDevices int64 `json:"provider_accepted_devices"`
	SubmissionFailedDevices int64 `json:"submission_failed_devices"`
	ObservedDevices         int64 `json:"observed_devices"`
	OpenedDevices           int64 `json:"opened_devices"`
	OpenedUsers             int64 `json:"opened_users"`
	FailedObservedDevices   int64 `json:"failed_observed_devices"`
}

type campaignStatistics struct {
	SchemaVersion       int             `json:"schema_version"`
	SnapshotComplete    bool            `json:"snapshot_complete"`
	ObservationCoverage string          `json:"observation_coverage"`
	Counts              *campaignCounts `json:"counts"`
}

func (s *Service) campaignStatistics(ctx context.Context, id pgtype.UUID, campaign int64) (campaignStatistics, error) {
	var out campaignStatistics
	scope, err := authz.NewApplicationAuthorizer(s.q).RequireApplication(ctx, id, authz.NotificationsRead)
	if err != nil {
		return out, err
	}
	row, err := s.q.GetCampaignStatistics(ctx, sqlc.GetCampaignStatisticsParams{ApplicationID: scope.ApplicationID, NotificationID: campaign})
	if err != nil {
		return out, campaignLookupError(err)
	}
	out = campaignStatistics{SchemaVersion: 1, SnapshotComplete: row.SnapshotComplete, ObservationCoverage: "reporting_devices_only"}
	if !row.SnapshotComplete {
		return out, nil
	}
	if row.LedgerDevices != row.TargetedDevices {
		return out, errors.New("inconsistent preparation snapshot")
	}
	out.Counts = &campaignCounts{TargetedUsers: row.TargetedUsers, EligibleUsers: row.EligibleUsers, TargetedDevices: row.TargetedDevices,
		PendingDevices: row.PendingDevices, ProviderAcceptedDevices: row.ProviderAcceptedDevices, SubmissionFailedDevices: row.SubmissionFailedDevices,
		ObservedDevices: row.ObservedDevices, OpenedDevices: row.OpenedDevices, OpenedUsers: row.OpenedUsers, FailedObservedDevices: row.FailedObservedDevices}
	return out, nil
}

// GetCampaignStatistics reads device evidence and its immutable denominators from one database snapshot.
func (h *Handler) GetCampaignStatistics(w http.ResponseWriter, r *http.Request) {
	id, campaign, err := campaignPath(r)
	if err != nil {
		writeError(w, err)
		return
	}
	stats, err := h.service.campaignStatistics(r.Context(), id, campaign)
	if err != nil {
		writeError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, stats, "success")
}
