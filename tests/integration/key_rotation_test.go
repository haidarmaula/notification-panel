//go:build integration

package integration

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/installation"
)

func TestKeyInventoryDuringBindingRotation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	actor := installation.Principal{ApplicationID: f.app, UserID: f.user}
	route := installation.Route{Namespace: "execution-fixture", Key: "route", Platform: "ANDROID", AddressType: installation.FCMRegistrationToken}
	protector, err := installation.NewProtector(2, map[int32][]byte{1: bytes.Repeat([]byte{1}, 32), 2: bytes.Repeat([]byte{3}, 32)}, bytes.Repeat([]byte{2}, 32))
	require.NoError(t, err)
	rotated, err := installation.NewLifecycle(f.pool, protector)
	require.NoError(t, err)
	refresh := installation.Change{Operation: installation.Refresh, InstallationID: f.enrollment.State.PublicID,
		ExpectedVersion: f.enrollment.State.Version, Proof: f.enrollment.Proof, Route: route, Address: "execution-address-0"}
	state, err := rotated.Apply(ctx, actor, refresh)
	require.NoError(t, err)
	require.Equal(t, f.enrollment.State, state)
	_, err = rotated.Enroll(ctx, actor, route, refresh.Address)
	require.ErrorIs(t, err, installation.ErrAddressClaimed)
	worker := executionService(t, f.pool)
	lease, err := worker.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	reservation, err := worker.Reserve(ctx, lease, f.deliveries[0])
	require.NoError(t, err)
	frozen, ok := reservation.ReceiptMaterial()
	require.True(t, ok)
	require.NoError(t, worker.Release(ctx, lease))
	refresh.Address = "rotated-address"
	state, err = rotated.Apply(ctx, actor, refresh)
	require.NoError(t, err)
	require.Equal(t, refresh.ExpectedVersion+1, state.Version)
	// An old writer cannot claim a new generation's address while writers overlap.
	_, err = f.lifecycle.Enroll(ctx, actor, route, refresh.Address)
	require.ErrorIs(t, err, installation.ErrAddressClaimed)

	query, err := os.ReadFile("../../scripts/sql/key-dependencies.sql")
	require.NoError(t, err)
	tx, err := f.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, string(query))
	require.NoError(t, err)
	type dependency struct {
		Application                   pgtype.UUID
		Purpose                       string
		Environment                   *string
		Key                           string
		Retained, Pending             int64
		Active, Unexpired, LastExpiry *int64
	}
	actual, err := pgx.CollectRows(rows, pgx.RowToStructByPos[dependency])
	require.NoError(t, err)
	one := int64(1)
	environment := "fixture"
	require.Equal(t, []dependency{
		{f.project, "binding_encryption", nil, "1", 2, 2, &one, nil, nil},
		{f.project, "binding_encryption", nil, "2", 1, 0, &one, nil, nil},
		{f.project, "receipt", &environment, frozen.KeyID, 1, 1, nil, &one, &frozen.Scope.ExpiresUnix},
	}, actual)
}
