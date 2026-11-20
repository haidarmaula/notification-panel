package applications

import (
	"net/http"
	"net/url"

	"github.com/jackc/pgx/v5/pgtype"

	"hello/internal/authz"
	"hello/internal/database/sqlc"
	"hello/internal/onboarding"
	"hello/pkg/response"
)

const testInstallationPageSize = 100

type testInstallation struct {
	InstallationID string  `json:"installation_id"`
	Platform       string  `json:"platform"`
	LastSeenAt     *string `json:"last_seen_at"`
}

type testInstallationsPage struct {
	Installations []testInstallation `json:"installations"`
	NextAfter     *string            `json:"next_after"`
}

type cohortTargetStatus struct {
	InstallationID string  `json:"installation_id"`
	Platform       string  `json:"platform"`
	Outcome        *string `json:"outcome"`
	FailureCode    *string `json:"failure_code"`
	CheckedAt      *string `json:"checked_at"`
}

// TestInstallationsHTTP exposes only installation IDs and safe presentation
// fields. Appearing in this list grants no test designation or send permission.
func (s *OnboardingService) TestInstallationsHTTP(w http.ResponseWriter, r *http.Request) {
	beginOnboardingResponse(w)
	id, ok := onboardingApplicationID(w, r)
	if !ok {
		return
	}
	cursor, err := testInstallationCursor(r.URL.RawQuery)
	if err != nil {
		writeOnboardingError(w, err)
		return
	}
	app, err := s.managed(r.Context(), id)
	if err != nil {
		writeOnboardingError(w, err)
		return
	}
	rows, err := s.base.q.ListOnboardingTestInstallations(r.Context(), sqlc.ListOnboardingTestInstallationsParams{
		ApplicationID: app.ApplicationID, CursorID: cursor,
	})
	if err != nil {
		writeOnboardingError(w, err)
		return
	}
	page := testInstallationsPage{Installations: make([]testInstallation, 0, min(testInstallationPageSize, len(rows)))}
	for _, row := range rows[:min(testInstallationPageSize, len(rows))] {
		page.Installations = append(page.Installations, testInstallation{
			InstallationID: row.InstallationID.String(), Platform: row.Platform, LastSeenAt: onboardingTime(row.LastSeenAt),
		})
	}
	if len(rows) > testInstallationPageSize {
		page.NextAfter = &page.Installations[len(page.Installations)-1].InstallationID
	}
	response.JSON(w, http.StatusOK, page, "ok")
}

func testInstallationCursor(rawQuery string) (pgtype.UUID, error) {
	if rawQuery == "" {
		return pgtype.UUID{Valid: true}, nil
	}
	query, err := url.ParseQuery(rawQuery)
	if err != nil || len(query) != 1 || len(query["after"]) != 1 {
		return pgtype.UUID{}, onboarding.ErrInvalidRequest
	}
	return onboardingID(query.Get("after"))
}

// CohortTargetsHTTP returns the per-target dry-run outcomes for a scoped
// TEST_COHORT operation. Pending targets have an unset outcome.
func (s *OnboardingService) CohortTargetsHTTP(w http.ResponseWriter, r *http.Request) {
	beginOnboardingResponse(w)
	id, ok := onboardingApplicationID(w, r)
	if !ok || !onboardingNoQuery(w, r) {
		return
	}
	operationID, ok := onboardingPathUUID(w, r.PathValue("operation_id"))
	if !ok {
		return
	}
	app, err := s.managed(r.Context(), id)
	if err != nil {
		writeOnboardingError(w, err)
		return
	}
	op, err := s.base.q.GetApplicationOnboardingOperation(r.Context(), sqlc.GetApplicationOnboardingOperationParams{
		ApplicationID: app.ApplicationID, ID: operationID,
	})
	if err != nil {
		writeOnboardingError(w, appError(err))
		return
	}
	if op.Kind != "TEST_COHORT" {
		writeOnboardingError(w, authz.ErrApplicationNotFound)
		return
	}
	rows, err := s.base.q.ListOnboardingCohortTargetStatus(r.Context(), sqlc.ListOnboardingCohortTargetStatusParams{
		ApplicationID: app.ApplicationID, OperationID: operationID,
	})
	if err != nil {
		writeOnboardingError(w, err)
		return
	}
	targets := make([]cohortTargetStatus, 0, len(rows))
	for _, row := range rows {
		var item = cohortTargetStatus{
			InstallationID: row.InstallationID.String(), Platform: row.Platform,
			CheckedAt: onboardingTime(row.CheckedAt),
		}
		if row.Outcome.Valid {
			item.Outcome = &row.Outcome.String
		}
		if row.FailureCode.Valid {
			item.FailureCode = &row.FailureCode.String
		}
		targets = append(targets, item)
	}
	response.JSON(w, http.StatusOK, targets, "ok")
}
