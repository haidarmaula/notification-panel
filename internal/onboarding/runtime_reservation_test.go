package onboarding

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type reservationQueries struct {
	queries []string
	args    [][]any
	allowed bool
	err     error
	rowID   string
}

type reservationRow struct {
	queries *reservationQueries
	reserve bool
}

func (q *reservationQueries) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	q.queries = append(q.queries, query)
	q.args = append(q.args, args)
	return reservationRow{queries: q, reserve: strings.Contains(query, "SELECT public.reserve_")}
}

func (r reservationRow) Scan(dest ...any) error {
	if r.queries.err != nil {
		return r.queries.err
	}
	if r.reserve {
		return dest[0].(*pgtype.UUID).Scan(r.queries.rowID)
	}
	*dest[0].(*bool) = r.queries.allowed
	return nil
}

func reservationRequest() RuntimeReservationRequest {
	return RuntimeReservationRequest{
		ID: "44444444-4444-4444-8444-444444444444", SetupRevision: 3,
		OriginEpoch: "55555555-5555-4555-8555-555555555555", SourceEpoch: "66666666-6666-4666-8666-666666666666",
		PolicySHA256: sha256.Sum256([]byte(validRuntimePolicy)),
	}
}

func TestRuntimeReservationFencesHostPlanAndDatabaseAuthority(t *testing.T) {
	request := reservationRequest()
	plan := preparationPlan(t)
	q := &reservationQueries{allowed: true, rowID: request.ID}
	reservation, err := ReserveRuntime(context.Background(), q, plan, request)
	if err != nil {
		t.Fatal(err)
	}
	if err = reservation.Check(context.Background(), plan.Delivery()); err != nil {
		t.Fatal(err)
	}
	// A caller cannot reuse a valid reservation for another image, app or generation.
	for _, change := range []func(*RuntimeDeliveryPlan){
		func(p *RuntimeDeliveryPlan) { p.ImageDigest = "unapproved" },
		func(p *RuntimeDeliveryPlan) { p.ApplicationID = request.ID },
		func(p *RuntimeDeliveryPlan) { p.Generation++ },
		func(p *RuntimeDeliveryPlan) { p.DatabaseConnections++ },
	} {
		changed := plan.Delivery()
		change(&changed)
		if !errors.Is(reservation.Check(context.Background(), changed), ErrRuntimeReservationUnavailable) {
			t.Fatal("mismatched plan was allowed")
		}
	}
	if len(q.queries) != 2 {
		t.Fatal("mismatched plan reached privileged database function")
	}
	q.allowed = false
	if !errors.Is(reservation.Check(context.Background(), plan.Delivery()), ErrRuntimeReservationUnavailable) {
		t.Fatal("revoked, expired or stale database scope was allowed")
	}
	q.err = errors.New("password=secret fixture")
	if got := reservation.Check(context.Background(), plan.Delivery()); got != ErrRuntimeReservationUnavailable {
		t.Fatalf("database diagnostic escaped safe boundary: %v", got)
	}
	for _, query := range q.queries {
		if strings.Contains(query, "release_") {
			t.Fatal("failure freed uncertain capacity")
		}
	}
}

func TestRuntimeReservationRejectsInvalidOrUnexpectedReservation(t *testing.T) {
	request := reservationRequest()
	q := &reservationQueries{rowID: request.ID}
	invalid := request
	invalid.PolicySHA256 = [32]byte{}
	if _, err := ReserveRuntime(context.Background(), q, preparationPlan(t), invalid); err != ErrRuntimeReservationUnavailable || len(q.queries) != 0 {
		t.Fatal("missing immutable policy authority reached database")
	}
	invalid.PolicySHA256 = [32]byte{1}
	if _, err := ReserveRuntime(context.Background(), q, preparationPlan(t), invalid); err != ErrRuntimeReservationUnavailable || len(q.queries) != 0 {
		t.Fatal("plan from different protected policy reached database")
	}
	q.rowID = "77777777-7777-4777-8777-777777777777"
	if _, err := ReserveRuntime(context.Background(), q, preparationPlan(t), request); err != ErrRuntimeReservationUnavailable {
		t.Fatal("unexpected reservation accepted")
	}
}
