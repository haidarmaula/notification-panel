package onboarding

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/fcm"
	"hello/internal/installation"
)

type cohortStoreStub struct {
	claim  func(pgtype.UUID, Environment, pgtype.UUID) *CohortTargetClaim
	finish func(CohortTargetFence, CohortTargetEvidence) bool
}

func (s cohortStoreStub) ClaimCohortTarget(_ context.Context, app pgtype.UUID, env Environment, owner pgtype.UUID) (*CohortTargetClaim, error) {
	return s.claim(app, env, owner), nil
}

func (s cohortStoreStub) FinishCohortTarget(_ context.Context, fence CohortTargetFence, evidence CohortTargetEvidence) (bool, error) {
	return s.finish(fence, evidence), nil
}

func TestCohortVerifier(t *testing.T) {
	for _, tt := range []struct {
		name              string
		wrongScope, stale bool
		provider          error
		want              CohortTargetEvidence
	}{
		{name: "accepted", want: CohortTargetEvidence{Outcome: "PASSED"}},
		{name: "unregistered", provider: fcm.ErrTargetUnregistered, want: CohortTargetEvidence{Outcome: "FAILED", FailureCode: "FCM_TARGET_UNREGISTERED"}},
		{name: "temporary", provider: fcm.ErrVerificationTemporary, want: CohortTargetEvidence{Outcome: "INCONCLUSIVE", FailureCode: "FCM_CHECK_INCONCLUSIVE"}},
		{name: "stale", stale: true, want: CohortTargetEvidence{Outcome: "PASSED"}},
		{name: "foreign scope", wrongScope: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app := pgtype.UUID{Valid: true, Bytes: [16]byte{1}}
			id := pgtype.UUID{Valid: true, Bytes: [16]byte{2}}
			var fence CohortTargetFence
			store := cohortStoreStub{
				claim: func(gotApp pgtype.UUID, env Environment, owner pgtype.UUID) *CohortTargetClaim {
					if gotApp != app || env != EnvironmentStaging {
						t.Fatal("unexpected claim scope")
					}
					fence = CohortTargetFence{ApplicationID: app, Environment: env, LeaseOwner: owner, LeaseToken: 1, BindingEpoch: 1,
						OperationID: id, CohortID: id, ApprovalID: id, ConfigurationID: id, InstallationID: id}
					if tt.wrongScope {
						fence.ApplicationID = id
					}
					return &CohortTargetClaim{Fence: fence, FileReference: id, Project: "example-project", ServiceAccountEmail: "account@example-project.iam.gserviceaccount.com", AddressType: installation.FCMFID, Address: "example-fid"}
				},
				finish: func(gotFence CohortTargetFence, got CohortTargetEvidence) bool {
					if tt.wrongScope || gotFence != fence || got != tt.want {
						t.Fatalf("unexpected evidence: %+v", got)
					}
					return !tt.stale
				},
			}
			v, err := NewCohortVerifier(store, VerifierPolicy{ApplicationID: app, Environment: EnvironmentStaging, CredentialRoot: "/protected"})
			if err != nil {
				t.Fatal(err)
			}
			called := false
			v.validate = func(_ context.Context, p fcm.FileCredentialPolicy, project, kind, address string) error {
				called = true
				if p.Path != "/protected/"+id.String()+"/service-account.json" || project != "example-project" || kind != installation.FCMFID || address != "example-fid" {
					t.Fatal("unexpected dry-run target")
				}
				return tt.provider
			}
			ran, err := v.RunOnce(context.Background())
			if !ran || called == tt.wrongScope || (err != nil) != (tt.wrongScope || tt.stale) || (err != nil && !errors.Is(err, ErrVerifierUnavailable)) {
				t.Fatalf("ran=%v, called=%v, err=%v", ran, called, err)
			}
		})
	}
}
