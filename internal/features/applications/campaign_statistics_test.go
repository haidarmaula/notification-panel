package applications

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
	"net/http"
	"net/http/httptest"
	"testing"
)

type statisticsFixture struct {
	scopedReadFixture
	row sqlc.GetCampaignStatisticsRow
}

func (f statisticsFixture) GetCampaignStatistics(_ context.Context, p sqlc.GetCampaignStatisticsParams) (sqlc.GetCampaignStatisticsRow, error) {
	if p.ApplicationID != 42 || p.NotificationID != 9 {
		return sqlc.GetCampaignStatisticsRow{}, pgx.ErrNoRows
	}
	return f.row, nil
}

func TestStatisticsSnapshotAvailability(t *testing.T) {
	const app = "7a740442-d2b9-4d60-b578-8bf05022d68e"
	var id pgtype.UUID
	require.NoError(t, id.Scan(app))
	for _, tc := range []struct {
		name  string
		row   sqlc.GetCampaignStatisticsRow
		grant bool
		code  int
	}{
		{"unavailable", sqlc.GetCampaignStatisticsRow{}, true, 200},
		{"known empty", sqlc.GetCampaignStatisticsRow{SnapshotComplete: true}, true, 200},
		{"damaged ledger", sqlc.GetCampaignStatisticsRow{SnapshotComplete: true, TargetedDevices: 1}, true, 500},
		{"missing grant", sqlc.GetCampaignStatisticsRow{}, false, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandler(&Service{q: statisticsFixture{scopedReadFixture{id: id, status: "ACTIVE", grant: tc.grant}, tc.row}})
			r := httptest.NewRequest(http.MethodGet, "/statistics", nil)
			r.SetPathValue("app_id", app)
			r.SetPathValue("campaign_id", "9")
			r = r.WithContext(context.WithValue(r.Context(), middleware.UserContextKey, &token.AccessClaims{StaffID: 7}))
			w := httptest.NewRecorder()
			h.GetCampaignStatistics(w, r)
			require.Equal(t, tc.code, w.Code)
			if tc.code == 200 {
				var v struct {
					Data campaignStatistics `json:"data"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
				require.Equal(t, tc.row.SnapshotComplete, v.Data.SnapshotComplete)
				require.Equal(t, tc.row.SnapshotComplete, v.Data.Counts != nil)
			}
			if tc.code == 500 {
				require.Contains(t, w.Body.String(), "internal server error")
			}
		})
	}
}
