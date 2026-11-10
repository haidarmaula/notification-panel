package applications

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

func TestDeliveryPagination(t *testing.T) {
	const cursor = "12345678-abcd-4000-8000-000000000001"
	for _, tc := range []struct {
		query string
		valid bool
	}{
		{"", true}, {"?limit=100", true}, {"?after_id=" + cursor + "&limit=1", true},
		{"?after_id=", false}, {"?after_id=0", false}, {"?after_id=24", false},
		{"?after_id=00000000-0000-0000-0000-000000000000", false},
		{"?after_id=12345678-ABCD-4000-8000-000000000001", false},
		{"?after_id=12345678abcd40008000000000000001", false},
		{"?after_id=urn:uuid:" + cursor, false}, {"?after_id=not-a-uuid", false},
		{"?after_id=" + cursor + "&after_id=" + cursor, false},
		{"?limit=0", false}, {"?limit=101", false}, {"?limit=-1", false},
		{"?limit=01", false}, {"?limit=2147483648", false}, {"?limit=", false},
		{"?limit=1&limit=2", false}, {"?unexpected=1", false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			p, err := deliveryPagination(httptest.NewRequest(http.MethodGet, "/"+tc.query, nil))
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.True(t, p.after.Valid)
			if tc.query == "" {
				require.Equal(t, [16]byte{}, p.after.Bytes)
				require.EqualValues(t, 20, p.limit)
			}
		})
	}
	// Read history keeps the existing decimal cursor contract.
	p, err := readPagination(httptest.NewRequest(http.MethodGet, "/?after_id=24", nil))
	require.NoError(t, err)
	require.EqualValues(t, 24, p.after)
	_, err = readPagination(httptest.NewRequest(http.MethodGet, "/?after_id="+cursor, nil))
	require.Error(t, err)
}

type historyFixture struct {
	scopedReadFixture
	t     *testing.T
	rows  []sqlc.ListScopedCampaignDeliveriesRow
	after pgtype.UUID
	fail  bool
}

func (q historyFixture) GetScopedCampaignID(_ context.Context, arg sqlc.GetScopedCampaignIDParams) (int64, error) {
	if arg.ApplicationID == 42 && arg.CampaignID == 9 {
		return 9, nil
	}
	return 0, pgx.ErrNoRows
}

func (q historyFixture) ListScopedCampaignDeliveries(_ context.Context, arg sqlc.ListScopedCampaignDeliveriesParams) ([]sqlc.ListScopedCampaignDeliveriesRow, error) {
	require.Equal(q.t, sqlc.ListScopedCampaignDeliveriesParams{ApplicationID: 42, CampaignID: 9, AfterID: q.after, PageSize: 2}, arg)
	if q.fail {
		return nil, pgx.ErrTxClosed
	}
	return q.rows, nil
}

func TestCampaignDeliveryHTTP(t *testing.T) {
	var app, first, second, installation pgtype.UUID
	require.NoError(t, app.Scan("7a740442-d2b9-4d60-b578-8bf05022d68e"))
	require.NoError(t, first.Scan("12345678-abcd-4000-8000-000000000001"))
	require.NoError(t, second.Scan("12345678-abcd-4000-8000-000000000002"))
	require.NoError(t, installation.Scan("12345678-abcd-4000-8000-000000000003"))
	stamp := pgtype.Timestamptz{Time: time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC), Valid: true}
	row := sqlc.ListScopedCampaignDeliveriesRow{ID: first, UserID: 9007199254740993, InstallationID: installation, Status: "OPENED", SentAt: stamp, DeliveredAt: stamp, OpenedAt: stamp}
	for _, tc := range []struct {
		name, campaign, query string
		rows                  []sqlc.ListScopedCampaignDeliveriesRow
		after                 pgtype.UUID
		code                  int
		fail                  bool
	}{
		{"limit plus one", "9", "?limit=1", []sqlc.ListScopedCampaignDeliveriesRow{row, {ID: second}}, pgtype.UUID{Valid: true}, 200, false},
		{"terminal", "9", "?limit=1&after_id=" + first.String(), []sqlc.ListScopedCampaignDeliveriesRow{{ID: second, InstallationID: installation, Status: "FAILED"}}, first, 200, false},
		{"empty", "9", "?limit=1", nil, pgtype.UUID{Valid: true}, 200, false},
		{"foreign campaign", "10", "?limit=1", nil, pgtype.UUID{Valid: true}, 404, false},
		{"bad cursor", "9", "?after_id=24", nil, pgtype.UUID{}, 400, false},
		{"database failure", "9", "?limit=1", nil, pgtype.UUID{Valid: true}, 500, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := historyFixture{scopedReadFixture: scopedReadFixture{id: app, status: "ACTIVE", grant: true}, t: t, rows: tc.rows, after: tc.after, fail: tc.fail}
			h := NewHandler(&Service{q: q})
			r := httptest.NewRequest(http.MethodGet, "/deliveries"+tc.query, nil)
			r.SetPathValue("app_id", app.String())
			r.SetPathValue("campaign_id", tc.campaign)
			r = r.WithContext(context.WithValue(r.Context(), middleware.UserContextKey, &token.AccessClaims{StaffID: 7}))
			w := httptest.NewRecorder()
			h.ListCampaignDeliveries(w, r)
			require.Equal(t, tc.code, w.Code, w.Body.String())
			if tc.code != 200 {
				if tc.code == 500 {
					require.Contains(t, w.Body.String(), "internal server error")
					require.NotContains(t, w.Body.String(), pgx.ErrTxClosed.Error())
				}
				return
			}
			var result struct {
				Data readPage[map[string]any] `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
			if tc.name == "limit plus one" {
				require.Equal(t, first.String(), *result.Data.NextAfterID)
				require.Len(t, result.Data.Items, 1)
				require.JSONEq(t, `{"id":"12345678-abcd-4000-8000-000000000001","user_id":"9007199254740993","installation_id":"12345678-abcd-4000-8000-000000000003","status":"OPENED","sent_at":"2026-11-07T08:00:00Z","delivered_at":"2026-11-07T08:00:00Z","opened_at":"2026-11-07T08:00:00Z"}`, mustHistoryJSON(t, result.Data.Items[0]))
			} else {
				require.Nil(t, result.Data.NextAfterID)
				if tc.name == "empty" {
					require.NotNil(t, result.Data.Items)
					require.Empty(t, result.Data.Items)
				}
			}
		})
	}
}

func mustHistoryJSON(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	require.NoError(t, err)
	return string(b)
}
