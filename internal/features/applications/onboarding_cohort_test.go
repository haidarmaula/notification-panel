package applications

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"hello/internal/database/sqlc"
)

type cohortAPIFixture struct {
	onboardingReads
	installations []sqlc.ListOnboardingTestInstallationsRow
	listed        sqlc.ListOnboardingTestInstallationsParams
	opKind        string
	targetReads   int
}

func (q *cohortAPIFixture) ListOnboardingTestInstallations(_ context.Context, args sqlc.ListOnboardingTestInstallationsParams) ([]sqlc.ListOnboardingTestInstallationsRow, error) {
	q.listed = args
	return q.installations, nil
}

func (q *cohortAPIFixture) GetApplicationOnboardingOperation(_ context.Context, args sqlc.GetApplicationOnboardingOperationParams) (sqlc.ApplicationOnboardingOperation, error) {
	if args.ApplicationID != q.row.ApplicationID || q.opKind == "" {
		return sqlc.ApplicationOnboardingOperation{}, pgx.ErrNoRows
	}
	return sqlc.ApplicationOnboardingOperation{Kind: q.opKind}, nil
}

func (q *cohortAPIFixture) ListOnboardingCohortTargetStatus(_ context.Context, _ sqlc.ListOnboardingCohortTargetStatusParams) ([]sqlc.ListOnboardingCohortTargetStatusRow, error) {
	q.targetReads++
	return nil, nil
}

func TestOnboardingTestInstallationsReturnSafeKeysetPage(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	const targetID = "22222222-2222-4222-8222-222222222222"
	id, _ := onboardingID(targetID)
	q := &cohortAPIFixture{onboardingReads: onboardingReads{
		principalQueries: principalQueries{role: "SUPER_ADMIN", active: true},
		row:              sqlc.GetApplicationOnboardingRow{ApplicationID: 62},
	}}
	for range testInstallationPageSize + 1 {
		q.installations = append(q.installations, sqlc.ListOnboardingTestInstallationsRow{InstallationID: id, Platform: "ANDROID"})
	}
	service, err := NewOnboardingService(q, panicDB{}, setupSettings())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/?after="+targetID, nil).WithContext(staffContext())
	req.SetPathValue("app_id", appID)
	w := httptest.NewRecorder()
	service.TestInstallationsHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, sqlc.ListOnboardingTestInstallationsParams{ApplicationID: 62, CursorID: id}, q.listed)
	var envelope struct {
		Data testInstallationsPage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data.Installations, testInstallationPageSize)
	require.Equal(t, targetID, *envelope.Data.NextAfter)
	for _, raw := range []string{"after=bad", "other=bad", "after=" + targetID + "&after=" + targetID} {
		_, err := testInstallationCursor(raw)
		require.Error(t, err, raw)
	}
}

func TestOnboardingCohortTargetsHideMissingAndOtherOperationKinds(t *testing.T) {
	for _, kind := range []string{"", "VERIFY", "TEST_COHORT"} {
		q := &cohortAPIFixture{opKind: kind, onboardingReads: onboardingReads{
			principalQueries: principalQueries{role: "SUPER_ADMIN", active: true},
			row:              sqlc.GetApplicationOnboardingRow{ApplicationID: 62},
		}}
		service, err := NewOnboardingService(q, panicDB{}, setupSettings())
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(staffContext())
		req.SetPathValue("app_id", "11111111-1111-4111-8111-111111111111")
		req.SetPathValue("operation_id", "22222222-2222-4222-8222-222222222222")
		w := httptest.NewRecorder()
		service.CohortTargetsHTTP(w, req)
		if kind != "TEST_COHORT" {
			require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
			require.Zero(t, q.targetReads)
		} else {
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Equal(t, 1, q.targetReads)
			require.Contains(t, w.Body.String(), `"data":[]`)
		}
	}
}
