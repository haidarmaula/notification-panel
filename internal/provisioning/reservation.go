package provisioning

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
)

// ErrReservationDenied is returned for invalid, expired, mismatched and
// unavailable reservations without exposing database details or supplied IDs.
var ErrReservationDenied = errors.New("reservation verification failed")

// ReservationQuerier is the only database operation used by the provisioner.
type ReservationQuerier interface {
	VerifyApplicationFCMReservation(context.Context, sqlc.VerifyApplicationFCMReservationParams) (bool, error)
}

// DBReservationVerifier checks one exact reserved candidate against PostgreSQL.
type DBReservationVerifier struct {
	queries ReservationQuerier
}

// NewDBReservationVerifier requires the narrow reservation query dependency.
func NewDBReservationVerifier(queries ReservationQuerier) (*DBReservationVerifier, error) {
	if queries == nil {
		return nil, ErrReservationDenied
	}
	return &DBReservationVerifier{queries: queries}, nil
}

// VerifyStage accepts only canonical IDs, supported environments and exact
// bounded credential identity values held by a live reservation.
func (v *DBReservationVerifier) VerifyStage(ctx context.Context, appID, reservationID, configID, environment, targetProject, expectedEmail string) error {
	if v == nil || v.queries == nil || ctx == nil || ctx.Err() != nil ||
		(environment != "staging" && environment != "production") || !boundedIdentity(targetProject) || !boundedIdentity(expectedEmail) {
		return ErrReservationDenied
	}
	app, okApp := canonicalDatabaseUUID(appID)
	reservation, okReservation := canonicalDatabaseUUID(reservationID)
	config, okConfig := canonicalDatabaseUUID(configID)
	if !okApp || !okReservation || !okConfig {
		return ErrReservationDenied
	}
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	valid, err := v.queries.VerifyApplicationFCMReservation(queryCtx, sqlc.VerifyApplicationFCMReservationParams{
		PublicID: app, ReservationID: reservation, ConfigurationID: config,
		Environment: environment, TargetProjectID: targetProject, ServiceAccountEmail: expectedEmail,
	})
	if err != nil || !valid || queryCtx.Err() != nil {
		return ErrReservationDenied
	}
	return nil
}

func canonicalDatabaseUUID(value string) (pgtype.UUID, bool) {
	var id pgtype.UUID
	if !canonicalUUID(value) || id.Scan(value) != nil || !id.Valid || id.Bytes == [16]byte{} || id.String() != value {
		return pgtype.UUID{}, false
	}
	return id, true
}

func boundedIdentity(value string) bool {
	if len(value) == 0 || len(value) > 255 || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
