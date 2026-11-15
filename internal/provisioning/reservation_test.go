package provisioning

import (
	"context"
	"errors"
	"testing"
	"time"

	"hello/internal/database/sqlc"
)

type reservationQueryStub struct {
	valid    bool
	err      error
	calls    int
	args     sqlc.VerifyApplicationFCMReservationParams
	deadline time.Time
}

func (q *reservationQueryStub) VerifyApplicationFCMReservation(ctx context.Context, args sqlc.VerifyApplicationFCMReservationParams) (bool, error) {
	q.calls++
	q.args = args
	q.deadline, _ = ctx.Deadline()
	return q.valid, q.err
}

func TestDBReservationVerifierPassesExactCandidateToNarrowQuery(t *testing.T) {
	query := &reservationQueryStub{valid: true}
	verifier, err := NewDBReservationVerifier(query)
	if err != nil {
		t.Fatal("NewDBReservationVerifier rejected a query")
	}
	before := time.Now()
	err = verifier.VerifyStage(context.Background(), testAppID, testReservationID, testConfigID, "production", "fixture-project", "fixture@fixture-project.iam.gserviceaccount.com")
	if err != nil {
		t.Fatalf("VerifyStage rejected a live reservation: %v", err)
	}
	if query.calls != 1 || !query.args.PublicID.Valid || query.args.PublicID.String() != testAppID ||
		query.args.ReservationID.String() != testReservationID || query.args.ConfigurationID.String() != testConfigID ||
		query.args.Environment != "production" || query.args.TargetProjectID != "fixture-project" ||
		query.args.ServiceAccountEmail != "fixture@fixture-project.iam.gserviceaccount.com" {
		t.Fatal("verifier did not pass the exact reserved identity to the query")
	}
	if query.deadline.IsZero() || query.deadline.After(before.Add(2*time.Second+time.Millisecond)) || query.deadline.Before(before.Add(time.Second)) {
		t.Fatal("reservation query did not receive a two-second deadline")
	}
}

func TestDBReservationVerifierDeniesInvalidOrUnavailableReservationsSafely(t *testing.T) {
	query := &reservationQueryStub{valid: false}
	verifier, err := NewDBReservationVerifier(query)
	if err != nil {
		t.Fatal("NewDBReservationVerifier rejected a query")
	}
	verify := func(app, reservation, config, env, project, email string) error {
		return verifier.VerifyStage(context.Background(), app, reservation, config, env, project, email)
	}
	cases := []struct{ name, app, reservation, config, env, project, email string }{
		{"zero application UUID", "00000000-0000-0000-0000-000000000000", testReservationID, testConfigID, "staging", "p", "a@b"},
		{"uppercase UUID", "11111111-1111-4111-8111-11111111111A", testReservationID, testConfigID, "staging", "p", "a@b"},
		{"path traversal UUID", "../" + testAppID, testReservationID, testConfigID, "staging", "p", "a@b"},
		{"unknown environment", testAppID, testReservationID, testConfigID, "development", "p", "a@b"},
		{"control identity", testAppID, testReservationID, testConfigID, "staging", "project\n", "a@b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := verify(tc.app, tc.reservation, tc.config, tc.env, tc.project, tc.email); !errors.Is(err, ErrReservationDenied) {
				t.Fatal("verifier did not return sanitized denial")
			}
		})
	}
	if query.calls != 0 {
		t.Fatal("invalid values reached the database query")
	}
	if err := verify(testAppID, testReservationID, testConfigID, "staging", "project", "service@example.test"); !errors.Is(err, ErrReservationDenied) {
		t.Fatal("verifier accepted an unreserved candidate")
	}
	query.valid = true
	query.err = errors.New("query error with sensitive details")
	if err := verify(testAppID, testReservationID, testConfigID, "staging", "project", "service@example.test"); !errors.Is(err, ErrReservationDenied) || err.Error() != ErrReservationDenied.Error() {
		t.Fatal("database error details escaped reservation verification")
	}
}

func TestDBReservationVerifierHonorsCanceledContextAndRequiresQuery(t *testing.T) {
	if _, err := NewDBReservationVerifier(nil); !errors.Is(err, ErrReservationDenied) {
		t.Fatal("missing query was accepted")
	}
	query := &reservationQueryStub{valid: true}
	verifier, _ := NewDBReservationVerifier(query)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := verifier.VerifyStage(ctx, testAppID, testReservationID, testConfigID, "staging", "project", "service@example.test"); !errors.Is(err, ErrReservationDenied) {
		t.Fatal("canceled verification was accepted")
	}
	if query.calls != 0 {
		t.Fatal("canceled context reached query")
	}
}
