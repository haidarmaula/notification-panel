//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"hello/internal/database"
	"hello/internal/database/sqlc"
	"hello/internal/features/applications"
)

// Each service transaction is a savepoint inside an isolated fixture transaction.
type segmentWriteFixture struct{ pgx.Tx }

func (f segmentWriteFixture) BeginTx(ctx context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	return f.Begin(ctx)
}

func TestScopedSegmentWrites(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(context.Background())
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := tx.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	number := func(sql string, args ...any) int64 {
		t.Helper()
		var id int64
		require.NoError(t, tx.QueryRow(ctx, sql, args...).Scan(&id))
		return id
	}
	staff := number("INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Write fixture','scope-writes@example.invalid','fixture' FROM roles WHERE name='SUPER_ADMIN' RETURNING id")
	var appID int64
	var public string
	require.NoError(t, tx.QueryRow(ctx, "INSERT INTO applications(key,name) VALUES('scope-writes','Writes') RETURNING id,public_id::text").Scan(&appID, &public))
	other := number("INSERT INTO applications(key,name) VALUES('scope-other','Other') RETURNING id")
	user := number("INSERT INTO users(application_id,external_id) VALUES($1,'scoped-write-user') RETURNING id", appID)
	foreign := number("INSERT INTO users(application_id,external_id) VALUES($1,'scoped-write-foreign') RETURNING id", other)
	exec("INSERT INTO application_staff_grants(application_id,staff_id) VALUES($1,$2)", appID, staff)
	mux := http.NewServeMux()
	q := sqlc.New(tx)
	actor := fixtureStaffContext(t, ctx, q, staff)
	applications.NewModule(q, segmentWriteFixture{tx}).RegisterRoutes(mux)
	base := "/api/v1/apps/" + public + "/segments"
	request := func(method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(actor)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		require.Equal(t, status, w.Code, w.Body.String())
		return w
	}
	request(http.MethodPost, base, `{"name":"Audience"}`, 409)
	// The production schema only accepts PROVISIONING. Exercise the future
	// ACTIVE path inside this rollback-only transaction.
	exec("ALTER TABLE applications DROP CONSTRAINT applications_status_check")
	exec("UPDATE applications SET status='ACTIVE' WHERE id=$1", appID)
	exec("ALTER TABLE audit_logs ADD CONSTRAINT reject_fixture_create CHECK(action <> 'SEGMENT_CREATE') NOT VALID")
	request(http.MethodPost, base, `{"name":"Audience"}`, 500)
	require.Zero(t, number("SELECT count(*) FROM segments WHERE application_id=$1", appID))
	exec("ALTER TABLE audit_logs DROP CONSTRAINT reject_fixture_create")

	w := request(http.MethodPost, base, `{"name":"Audience","description":"test"}`, 201)
	var envelope struct {
		Data struct {
			ID string `json:"id"`
		}
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	path := base + "/" + envelope.Data.ID
	request(http.MethodPost, base, `{"name":"Audience"}`, 409)
	request(http.MethodPut, path, `{"name":"Renamed"}`, 200)
	request(http.MethodPut, fmt.Sprintf("%s/members/%d", path, foreign), "", 404)
	memberPath := fmt.Sprintf("%s/members/%d", path, user)
	request(http.MethodPut, memberPath, "", 204)
	request(http.MethodPut, memberPath, "", 409)
	request(http.MethodDelete, path, "", 409)
	request(http.MethodDelete, memberPath, "", 204)
	request(http.MethodDelete, memberPath, "", 404)
	request(http.MethodDelete, path, "", 204)
	require.Zero(t, number("SELECT count(*) FROM segments WHERE application_id=$1", appID))
	require.EqualValues(t, 5, number("SELECT count(*) FROM audit_logs WHERE application_id=$1 AND actor_user_id=$2", appID, staff))
	{ // Campaign draft references and rollback.
		campaigns := "/api/v1/apps/" + public + "/campaigns"
		template := number("INSERT INTO templates(application_id,name,title_template,body_template,created_by) VALUES($1,'Template','Title','Body',$2) RETURNING id", appID, staff)
		foreignTemplate := number("INSERT INTO templates(application_id,name,title_template,body_template,created_by) VALUES($1,'Foreign','Title','Body',$2) RETURNING id", other, staff)
		segment := number("INSERT INTO segments(application_id,name,created_by) VALUES($1,'Local campaign segment',$2) RETURNING id", appID, staff)
		foreignSegment := number("INSERT INTO segments(application_id,name,created_by) VALUES($1,'Foreign campaign segment',$2) RETURNING id", other, staff)
		draft := func(templateID, segmentID, userID int64) string {
			return fmt.Sprintf(`{"title":"Title","body":"Body","template_id":"%d","targets":[{"type":"SEGMENT","id":"%d"},{"type":"INDIVIDUAL","id":"%d"}]}`, templateID, segmentID, userID)
		}
		assertEmpty := func() {
			require.Zero(t, number("SELECT count(*) FROM notifications WHERE application_id=$1", appID))
			require.Zero(t, number("SELECT count(*) FROM notification_targets WHERE application_id=$1", appID))
			require.Zero(t, number("SELECT count(*) FROM audit_logs WHERE application_id=$1 AND action='NOTIFICATION_CREATE'", appID))
		}
		request(http.MethodPost, campaigns, draft(foreignTemplate, segment, user), 404)
		request(http.MethodPost, campaigns, draft(template, foreignSegment, user), 404)
		request(http.MethodPost, campaigns, draft(template, segment, foreign), 404)
		// Go's case-insensitive struct decoding previously let ID override id.
		aliasedTarget := fmt.Sprintf(`{"title":"Title","body":"Body","targets":[{"type":"INDIVIDUAL","id":"%d","ID":"%d"}]}`, foreign, user)
		request(http.MethodPost, campaigns, aliasedTarget, 400)
		request(http.MethodPost, campaigns, `{"title":"Title","body":"Body","targets":[{"TYPE":"BROADCAST"}]}`, 400)
		assertEmpty()
		exec("ALTER TABLE audit_logs ADD CONSTRAINT reject_fixture_campaign CHECK(action <> 'NOTIFICATION_CREATE') NOT VALID")
		request(http.MethodPost, campaigns, draft(template, segment, user), 500)
		assertEmpty()
		exec("ALTER TABLE audit_logs DROP CONSTRAINT reject_fixture_campaign")
		request(http.MethodPost, campaigns, draft(template, segment, user), 201)
		require.EqualValues(t, 1, number("SELECT count(*) FROM notifications WHERE application_id=$1 AND status='DRAFT' AND template_id=$2 AND template_name='Template'", appID, template))
		require.EqualValues(t, 2, number("SELECT count(*) FROM notification_targets WHERE application_id=$1", appID))
		require.EqualValues(t, 1, number("SELECT count(*) FROM audit_logs WHERE application_id=$1 AND action='NOTIFICATION_CREATE'", appID))
		campaignID := number("SELECT id FROM notifications WHERE application_id=$1", appID)
		foreignCampaign := number("INSERT INTO notifications(application_id,title,body,created_by) VALUES($1,'Foreign draft','Body',$2) RETURNING id", other, staff)
		campaignPath := fmt.Sprintf("%s/%d", campaigns, campaignID)
		foreignPath := fmt.Sprintf("%s/%d", campaigns, foreignCampaign)
		request(http.MethodGet, foreignPath, "", 404)
		request(http.MethodPut, foreignPath, draft(template, segment, user), 404)
		request(http.MethodDelete, foreignPath, "", 404)
		require.NotContains(t, request(http.MethodGet, campaigns+"?limit=1", "", 200).Body.String(), "Foreign draft")
		require.Contains(t, request(http.MethodGet, campaignPath, "", 200).Body.String(), "INDIVIDUAL")

		request(http.MethodPut, campaignPath, draft(template, segment, foreign), 404)
		request(http.MethodPut, campaignPath, aliasedTarget, 400)
		require.EqualValues(t, 2, number("SELECT count(*) FROM notification_targets WHERE notification_id=$1", campaignID))
		require.Zero(t, number("SELECT count(*) FROM audit_logs WHERE application_id=$1 AND action='NOTIFICATION_UPDATE'", appID))
		exec("UPDATE notifications SET status='QUEUED' WHERE id=$1", campaignID)
		request(http.MethodPut, campaignPath, draft(template, segment, user), 409)
		request(http.MethodDelete, campaignPath, "", 409)
		exec("UPDATE notifications SET status='DRAFT' WHERE id=$1", campaignID)
		unassigned := number("INSERT INTO notification_targets(notification_id,target_type) VALUES($1,'BROADCAST') RETURNING id", campaignID)
		request(http.MethodDelete, campaignPath, "", 409)
		exec("DELETE FROM notification_targets WHERE id=$1", unassigned)
		request(http.MethodPut, campaignPath, `{"title":"Replaced","body":"Body","targets":[{"type":"BROADCAST"}]}`, 200)
		require.EqualValues(t, 1, number("SELECT count(*) FROM notifications WHERE id=$1 AND title='Replaced' AND template_id IS NULL AND template_name IS NULL", campaignID))
		require.EqualValues(t, 1, number("SELECT count(*) FROM notification_targets WHERE notification_id=$1 AND target_type='BROADCAST'", campaignID))
		request(http.MethodDelete, campaignPath, "", 204)
		request(http.MethodGet, campaignPath, "", 404)
		require.Zero(t, number("SELECT count(*) FROM notification_targets WHERE notification_id=$1", campaignID))
		require.EqualValues(t, 2, number("SELECT count(*) FROM audit_logs WHERE application_id=$1 AND action IN ('NOTIFICATION_UPDATE','NOTIFICATION_DELETE')", appID))

		templates := "/api/v1/apps/" + public + "/templates"
		templatePath := fmt.Sprintf("%s/%d", templates, template)
		foreignTemplatePath := fmt.Sprintf("%s/%d", templates, foreignTemplate)
		active := `{"name":"New template","title_template":"Title","body_template":"Body","is_active":true}`
		inactive := `{"name":"Retired","title_template":"Title","body_template":"Body","is_active":false}`
		request(http.MethodGet, foreignTemplatePath, "", 404)
		request(http.MethodPut, foreignTemplatePath, inactive, 404)
		require.NotContains(t, request(http.MethodGet, templates, "", 200).Body.String(), "Foreign")
		exec("ALTER TABLE audit_logs ADD CONSTRAINT reject_fixture_template CHECK(action <> 'TEMPLATE_CREATE') NOT VALID")
		request(http.MethodPost, templates, active, 500)
		require.EqualValues(t, 1, number("SELECT count(*) FROM templates WHERE application_id=$1", appID))
		exec("ALTER TABLE audit_logs DROP CONSTRAINT reject_fixture_template")
		request(http.MethodPost, templates, active, 201)
		request(http.MethodPost, campaigns, draft(template, segment, user), 201)
		request(http.MethodPut, templatePath, inactive, 200)
		request(http.MethodPost, campaigns, draft(template, segment, user), 404)
		require.EqualValues(t, 1, number("SELECT count(*) FROM notifications WHERE application_id=$1 AND template_id=$2", appID, template))
		require.EqualValues(t, 2, number("SELECT count(*) FROM audit_logs WHERE application_id=$1 AND entity_type='template'", appID))

		historyCampaign := number("SELECT id FROM notifications WHERE application_id=$1", appID)
		historyPath := fmt.Sprintf("%s/%d", campaigns, historyCampaign)
		request(http.MethodGet, foreignPath+"/deliveries", "", 404)
		request(http.MethodGet, foreignPath+"/reads", "", 404)
		require.Contains(t, request(http.MethodGet, historyPath+"/reads", "", 200).Body.String(), `"items":[]`)
		device := number("INSERT INTO device_tokens(application_id,user_id,platform,provider,push_token) VALUES($1,$2,'ANDROID','FCM','private-fixture-token') RETURNING id", appID, user)
		delivery := number("INSERT INTO notification_deliveries(application_id,notification_id,user_id,device_token_id,provider,provider_message_id,failed_reason) VALUES($1,$2,$3,$4,'FCM','private-provider-id','private-failure-detail') RETURNING id", appID, historyCampaign, user, device)
		exec("INSERT INTO notification_deliveries(notification_id,user_id,device_token_id,provider) VALUES($1,$2,$3,'LEGACY')", historyCampaign, user, device)
		exec("INSERT INTO notification_reads(application_id,notification_id,user_id) VALUES($1,$2,$3)", appID, historyCampaign, user)
		exec("INSERT INTO notification_reads(notification_id,user_id) VALUES($1,$2)", historyCampaign, foreign)
		var deliveries, reads struct {
			Data struct {
				Items       []map[string]any `json:"items"`
				NextAfterID *string          `json:"next_after_id"`
			} `json:"data"`
		}
		w = request(http.MethodGet, historyPath+"/deliveries?limit=1", "", 200)
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &deliveries))
		require.Len(t, deliveries.Data.Items, 1)
		require.Len(t, deliveries.Data.Items[0], 7)
		require.Equal(t, fmt.Sprint(delivery), deliveries.Data.Items[0]["id"])
		require.Nil(t, deliveries.Data.NextAfterID)
		w = request(http.MethodGet, historyPath+"/reads", "", 200)
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &reads))
		require.Len(t, reads.Data.Items, 1)
		require.Equal(t, fmt.Sprint(user), reads.Data.Items[0]["user_id"])

		uploads := "/api/v1/apps/" + public + "/uploads"
		batch := number("INSERT INTO upload_batches(application_id,uploaded_by,original_filename,total_rows,valid_rows) VALUES($1,$2,'private-file',1,1) RETURNING id", appID, staff)
		foreignBatch := number("INSERT INTO upload_batches(application_id,uploaded_by,original_filename) VALUES($1,$2,'foreign-file') RETURNING id", other, staff)
		exec("INSERT INTO upload_batch_rows(application_id,batch_id,external_id,error_message) VALUES($1,$2,'private-subject','private-error')", appID, batch)
		exec("INSERT INTO upload_batch_rows(batch_id,external_id) VALUES($1,'legacy-subject')", batch)
		request(http.MethodGet, fmt.Sprintf("%s/%d/rows", uploads, foreignBatch), "", 404)
		var page struct {
			Data struct {
				Items []map[string]any `json:"items"`
			} `json:"data"`
		}
		w = request(http.MethodGet, uploads, "", 200)
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
		require.Len(t, page.Data.Items, 1)
		require.Equal(t, fmt.Sprint(batch), page.Data.Items[0]["id"])
		require.NotContains(t, w.Body.String(), "private-file")
		page.Data.Items = nil
		w = request(http.MethodGet, fmt.Sprintf("%s/%d/rows", uploads, batch), "", 200)
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
		require.Len(t, page.Data.Items, 1)
		require.Len(t, page.Data.Items[0], 3)
		require.NotContains(t, w.Body.String(), "private-subject")
		require.NotContains(t, w.Body.String(), "private-error")

		audits := "/api/v1/apps/" + public + "/audit-logs"
		exec(`INSERT INTO audit_logs(application_id,actor_user_id,action,entity_type,before_json,ip_address) VALUES($1,$2,'OWN_MARKER','fixture','{"private":true}','private-ip')`, appID, staff)
		exec("INSERT INTO audit_logs(application_id,actor_user_id,action,entity_type) VALUES($1,$2,'FOREIGN_MARKER','fixture')", other, staff)
		exec("INSERT INTO audit_logs(actor_user_id,action,entity_type) VALUES($1,'GLOBAL_MARKER','fixture')", staff)
		w = request(http.MethodGet, audits, "", 200)
		require.Contains(t, w.Body.String(), "OWN_MARKER")
		for _, hidden := range []string{"FOREIGN_MARKER", "GLOBAL_MARKER", "private-ip", "before_json", "private"} {
			require.NotContains(t, w.Body.String(), hidden)
		}
		exec("UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='VIEWER') WHERE id=$1", staff)
		request(http.MethodGet, audits, "", 403)
		request(http.MethodGet, uploads, "", 200)

	}

}
