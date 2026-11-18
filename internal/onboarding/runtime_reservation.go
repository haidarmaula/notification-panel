package onboarding

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrRuntimeReservationUnavailable means no current authority exists for a host effect.
var ErrRuntimeReservationUnavailable = errors.New("runtime reservation unavailable")

// RuntimeReservationQueries is the restricted preparation database connection.
// Its LOGIN must be scoped by the owner-installed runtime preparation registry.
type RuntimeReservationQueries interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// RuntimeReservationRequest binds an operator-approved preparation to a setup snapshot.
// Reusing the reservation ID never renews its host-effect deadline.
type RuntimeReservationRequest struct {
	ID            string
	SetupRevision int64
	OriginEpoch   string
	SourceEpoch   string
	PolicySHA256  [32]byte
}

// RuntimeReservation retains capacity after timeouts, cancellation and process crashes.
// It is not a runtime acknowledgement or lifecycle/dispatch capability.
type RuntimeReservation struct {
	queries RuntimeReservationQueries
	plan    RuntimeDeliveryPlan
	request RuntimeReservationRequest
}

// ReserveRuntime reserves durable capacity before any Docker effect. Call Check
// immediately before effects; successful historical reservation replay alone is
// insufficient. Cleanup requires an independent observation that the runtime is absent.
func ReserveRuntime(ctx context.Context, q RuntimeReservationQueries, plan RuntimePlan, request RuntimeReservationRequest) (*RuntimeReservation, error) {
	delivery := plan.Delivery()
	if q == nil || !validRuntimeUUID(request.ID) || !validRuntimeUUID(request.OriginEpoch) ||
		!validRuntimeUUID(request.SourceEpoch) || request.SetupRevision <= 0 ||
		request.PolicySHA256 == [32]byte{} || request.PolicySHA256 != plan.policySHA256 || !validRuntimeUUID(delivery.ApplicationID) ||
		!validRuntimeUUID(delivery.ConfigurationID) || delivery.Generation <= 0 || delivery.PolicyRevision <= 0 {
		return nil, ErrRuntimeReservationUnavailable
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var reserved pgtype.UUID
	err := q.QueryRow(callCtx, `SELECT public.reserve_application_onboarding_runtime($1,$2,$3,$4,$5,$6,$7,$8)`,
		runtimeUUID(delivery.ApplicationID), runtimeUUID(request.ID), runtimeUUID(delivery.ConfigurationID),
		delivery.Generation, delivery.PolicyRevision, request.SetupRevision,
		runtimeUUID(request.OriginEpoch), runtimeUUID(request.SourceEpoch)).Scan(&reserved)
	if err != nil || !reserved.Valid || reserved.String() != request.ID {
		return nil, ErrRuntimeReservationUnavailable
	}
	return &RuntimeReservation{queries: q, plan: delivery, request: request}, nil
}

// Check fences host effects against the original immutable plan, current DB scope,
// profile digest, epochs and deadline. Failure never releases capacity implicitly.
func (r *RuntimeReservation) Check(ctx context.Context, plan RuntimeDeliveryPlan) error {
	if r == nil || r.queries == nil || plan != r.plan {
		return ErrRuntimeReservationUnavailable
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var allowed bool
	err := r.queries.QueryRow(callCtx, `SELECT public.check_application_onboarding_runtime($1,$2,$3)`,
		runtimeUUID(plan.ApplicationID), runtimeUUID(r.request.ID), r.request.PolicySHA256[:]).Scan(&allowed)
	if err != nil || !allowed {
		return ErrRuntimeReservationUnavailable
	}
	return nil
}

func runtimeUUID(value string) pgtype.UUID {
	var id pgtype.UUID
	_ = id.Scan(value) // All callers validate canonical UUIDs before reaching this boundary.
	return id
}
