//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/features/applications"
	"hello/internal/installation"
)

func TestScopedCampaignHistoryModernLedger(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := newObservationFixture(t, ctx, "execution-address-0")
	_, err := f.worker.RecordResult(ctx, f.result)
	require.NoError(t, err)
	deliveredReport := f.report
	deliveredReport.Event = "DELIVERED"
	deliveredReport.ClientEventID = replayUUID(t, ctx, f.pool)
	require.NoError(t, f.record(ctx, deliveredReport))
	require.NoError(t, f.record(ctx, f.report))
	var unrelatedCampaign int64
	require.NoError(t, f.pool.QueryRow(ctx, "INSERT INTO notifications(application_id,title,body,created_by) VALUES($1,'Unrelated history','Fixture',$2) RETURNING id", f.app, f.staff).Scan(&unrelatedCampaign))
	executionSQL(t, ctx, f.executionFixture, `INSERT INTO notification_delivery_intents(application_id,notification_id,installation_id,binding_id,owner_user_id,owner_epoch)
		SELECT application_id,$1,installation_id,binding_id,owner_user_id,owner_epoch FROM notification_delivery_intents WHERE id=$2`, unrelatedCampaign, f.report.DeliveryID)

	mux := http.NewServeMux()
	applications.NewModule(f.q, f.pool).RegisterRoutes(mux)
	actor := fixtureStaffContext(t, ctx, f.q, f.staff)
	base := fmt.Sprintf("/api/v1/apps/%s/campaigns/%d/deliveries", f.project.String(), f.campaign)
	type item struct {
		ID             string     `json:"id"`
		UserID         string     `json:"user_id"`
		InstallationID string     `json:"installation_id"`
		Status         string     `json:"status"`
		SentAt         *time.Time `json:"sent_at"`
		DeliveredAt    *time.Time `json:"delivered_at"`
		OpenedAt       *time.Time `json:"opened_at"`
	}
	type page struct {
		Data struct {
			Items []item  `json:"items"`
			Next  *string `json:"next_after_id"`
		} `json:"data"`
	}
	request := func(path string, code int) page {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil).WithContext(actor)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		require.Equal(t, code, w.Code, w.Body.String())
		for _, secret := range []string{f.report.Token, f.enrollment.Proof, "execution-address-0", "provider_message_id", "proof_verifier", "address_ciphertext", "credential", "terminal_reason", "failed_reason", "device_id", "binding_id"} {
			require.NotContains(t, w.Body.String(), secret)
		}
		var p page
		if code == 200 {
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
			var fields struct {
				Data struct {
					Items []map[string]any `json:"items"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &fields))
			for _, fields := range fields.Data.Items {
				require.Len(t, fields, 7)
				for _, name := range []string{"id", "user_id", "installation_id", "status", "sent_at", "delivered_at", "opened_at"} {
					require.Contains(t, fields, name)
				}
			}
		}
		return p
	}
	first := request(base+"?limit=1", 200)
	require.Len(t, first.Data.Items, 1)
	require.NotNil(t, first.Data.Next, "the query must fetch limit+1")
	require.Equal(t, first.Data.Items[0].ID, *first.Data.Next)
	last := request(base+"?limit=1&after_id="+*first.Data.Next, 200)
	require.Len(t, last.Data.Items, 1)
	require.Nil(t, last.Data.Next, "exactly one remaining item is terminal")
	require.Less(t, first.Data.Items[0].ID, last.Data.Items[0].ID)
	empty := request(base+"?limit=1&after_id="+last.Data.Items[0].ID, 200)
	require.Empty(t, empty.Data.Items)
	require.NotNil(t, empty.Data.Items)
	require.Nil(t, empty.Data.Next)
	for _, cursor := range []string{"", "24", "0", "not-a-uuid", "00000000-0000-0000-0000-000000000000", "12345678-ABCD-4000-8000-000000000001"} {
		request(base+"?after_id="+cursor, 400)
	}

	var sent, delivered, opened time.Time
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT provider_accepted_at,observed_delivered_at,observed_opened_at FROM notification_delivery_intents WHERE id=$1`, f.report.DeliveryID).Scan(&sent, &delivered, &opened))
	assertAttribution := func(p page) {
		t.Helper()
		require.Len(t, p.Data.Items, 2)
		found := false
		for _, got := range p.Data.Items {
			if got.ID != f.report.DeliveryID.String() {
				continue
			}
			found = true
			require.Equal(t, fmt.Sprint(f.user), got.UserID)
			require.Equal(t, f.enrollment.State.PublicID.String(), got.InstallationID)
			require.Equal(t, "OPENED", got.Status)
			require.NotNil(t, got.SentAt)
			require.NotNil(t, got.DeliveredAt)
			require.NotNil(t, got.OpenedAt)
			require.True(t, sent.Equal(*got.SentAt))
			require.True(t, delivered.Equal(*got.DeliveredAt))
			require.True(t, opened.Equal(*got.OpenedAt))
		}
		require.True(t, found)
	}
	assertAttribution(request(base, 200))
	var nextOwner int64
	require.NoError(t, f.pool.QueryRow(ctx, "INSERT INTO users(application_id,external_id) VALUES($1,'history-next-owner') RETURNING id", f.app).Scan(&nextOwner))
	_, err = f.lifecycle.Apply(ctx, installation.Principal{ApplicationID: f.app, UserID: nextOwner}, installation.Change{
		InstallationID: f.enrollment.State.PublicID, Proof: f.enrollment.Proof, ExpectedVersion: f.enrollment.State.Version,
		Operation: installation.SwitchOwner,
		Route:     installation.Route{Namespace: "execution-fixture", Key: "route", Platform: "ANDROID", AddressType: installation.FCMRegistrationToken},
		Address:   "execution-address-0",
	})
	require.NoError(t, err)
	assertAttribution(request(base, 200))

	// A grant to another application must not expose this campaign's history.
	var other int64
	var public pgtype.UUID
	require.NoError(t, f.pool.QueryRow(ctx, "INSERT INTO applications(key,name,status) VALUES('history-other','Other','ACTIVE') RETURNING id,public_id").Scan(&other, &public))
	executionSQL(t, ctx, f.executionFixture, "INSERT INTO application_staff_grants(application_id,staff_id) VALUES($1,$2)", other, f.staff)
	request(fmt.Sprintf("/api/v1/apps/%s/campaigns/%d/deliveries", public.String(), f.campaign), 404)

	// Non-observation states remain visible too; this is not a success-only feed.
	for _, status := range []string{"PENDING", "SENT", "DELIVERED", "OPENED", "FAILED"} {
		executionSQL(t, ctx, f.executionFixture, "UPDATE notification_delivery_intents SET status=$1 WHERE id=$2", status, f.report.DeliveryID)
		p := request(base, 200)
		for _, got := range p.Data.Items {
			if got.ID == f.report.DeliveryID.String() {
				require.Equal(t, status, got.Status)
			}
		}
	}
}
