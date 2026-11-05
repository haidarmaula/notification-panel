//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
	"hello/internal/receipt"
)

func receiptMetadataCount(t *testing.T, ctx context.Context, f executionFixture) int {
	t.Helper()
	var count int
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_receipt_metadata").Scan(&count))
	return count
}
func TestReceiptMetadataStableAcrossTakeoverAndRotation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	executionSQL(t, ctx, f, "UPDATE notifications SET deadline_at=date_trunc('second',clock_timestamp())+interval '5 minutes 0.125 seconds'")
	first := executionService(t, f.pool)
	lease, err := first.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	reservation, err := first.Reserve(ctx, lease, f.deliveries[0])
	require.NoError(t, err)
	original, ok := reservation.ReceiptMaterial()
	require.True(t, ok)
	var expected int64
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT ceil(extract(epoch FROM deadline_at))::bigint+86400 FROM notifications WHERE id=$1", f.campaign).Scan(&expected))
	require.Equal(t, expected, original.Scope.ExpiresUnix)
	require.Equal(t, "fixture-key", original.KeyID)
	require.Equal(t, f.project, original.Scope.ApplicationID)
	require.Equal(t, f.deliveries[0], original.Scope.DeliveryID)
	oldKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{11}, 32))
	newKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{12}, 32))
	signer, err := receipt.NewSigner("fixture", map[string]ed25519.PrivateKey{"fixture-key": oldKey, "rotated": newKey})
	require.NoError(t, err)
	token, err := signer.SignMaterial(original)
	require.NoError(t, err)
	copied := original
	copied.Scope.OwnerEpoch++
	again, ok := reservation.ReceiptMaterial()
	require.True(t, ok)
	require.NotEqual(t, copied, again)
	require.NoError(t, first.Release(ctx, lease))
	executionSQL(t, ctx, f, "UPDATE notification_delivery_intents SET next_attempt_at=clock_timestamp()-interval '1 second'")
	policy := executionPolicy()
	policy.Receipt.KeyID = "rotated"
	policy.Receipt.ObservationWindow = 72 * time.Hour
	replacement, err := dispatch.NewExecution(f.pool, policy)
	require.NoError(t, err)
	lease, err = replacement.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	reservation, err = replacement.Reserve(ctx, lease, f.deliveries[0])
	require.NoError(t, err)
	restored, ok := reservation.ReceiptMaterial()
	require.True(t, ok)
	require.Equal(t, original, restored)
	retried, err := signer.SignMaterial(restored)
	require.NoError(t, err)
	require.Equal(t, token.WireValue(), retried.WireValue())
	reservation, err = replacement.Reserve(ctx, lease, f.deliveries[1])
	require.NoError(t, err)
	fresh, ok := reservation.ReceiptMaterial()
	require.True(t, ok)
	require.Equal(t, "rotated", fresh.KeyID)
	require.Equal(t, original.Scope.ExpiresUnix+48*3600, fresh.Scope.ExpiresUnix)
	missingOld, err := receipt.NewSigner("fixture", map[string]ed25519.PrivateKey{"rotated": newKey})
	require.NoError(t, err)
	_, err = missingOld.SignMaterial(restored)
	require.ErrorIs(t, err, receipt.ErrInvalid)
	require.NoError(t, replacement.Release(ctx, lease))
	executionSQL(t, ctx, f, "UPDATE notification_delivery_intents SET next_attempt_at=clock_timestamp()-interval '1 second'")
	policy.Receipt.Environment = "foreign"
	foreign, err := dispatch.NewExecution(f.pool, policy)
	require.NoError(t, err)
	lease, err = foreign.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	reservation, err = foreign.Reserve(ctx, lease, f.deliveries[0])
	require.ErrorIs(t, err, dispatch.ErrExecutionHeld)
	require.Nil(t, reservation)
	require.Equal(t, 3, executionAttempts(t, ctx, f))
	require.Equal(t, 2, receiptMetadataCount(t, ctx, f))
}

func TestReceiptMetadataAndChargeCommitAtomically(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	uncertain, err := dispatch.NewExecution(&replayNthCommitFailure{Pool: f.pool}, executionPolicy())
	require.NoError(t, err)
	lease, err := uncertain.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	reservation, err := uncertain.Reserve(ctx, lease, f.deliveries[0])
	require.Error(t, err)
	require.Nil(t, reservation)
	_, ok := reservation.ReceiptMaterial()
	require.False(t, ok)
	require.Equal(t, 1, executionAttempts(t, ctx, f))
	require.Equal(t, 1, receiptMetadataCount(t, ctx, f))
	// Metadata insertion happens before the guarded charge; charge failure must
	// roll back that newly inserted row as well as the attempt mutation.
	executionSQL(t, ctx, f, `ALTER TABLE notification_delivery_intents ADD CONSTRAINT fixture_receipt_charge_reject CHECK (id<>'`+f.deliveries[1].String()+`'::uuid OR attempt_count=0)`)
	reservation, err = uncertain.Reserve(ctx, lease, f.deliveries[1])
	require.Error(t, err)
	require.Nil(t, reservation)
	require.Equal(t, 1, executionAttempts(t, ctx, f))
	require.Equal(t, 1, receiptMetadataCount(t, ctx, f))
	executionSQL(t, ctx, f, "ALTER TABLE notification_delivery_intents DROP CONSTRAINT fixture_receipt_charge_reject")
	// A failure in metadata persistence must not consume a device attempt either.
	executionSQL(t, ctx, f, "ALTER TABLE notification_receipt_metadata ADD CONSTRAINT fixture_receipt_insert_reject CHECK (delivery_id<>'"+f.deliveries[1].String()+"'::uuid)")
	reservation, err = uncertain.Reserve(ctx, lease, f.deliveries[1])
	require.Error(t, err)
	require.Nil(t, reservation)
	require.Equal(t, 1, executionAttempts(t, ctx, f))
	require.Equal(t, 1, receiptMetadataCount(t, ctx, f))
	executionSQL(t, ctx, f, "ALTER TABLE notification_receipt_metadata DROP CONSTRAINT fixture_receipt_insert_reject")
	reservation, err = uncertain.Reserve(ctx, lease, f.deliveries[1])
	require.NoError(t, err)
	require.NotNil(t, reservation)
	require.Equal(t, 2, executionAttempts(t, ctx, f))
	require.Equal(t, 2, receiptMetadataCount(t, ctx, f))
}

func TestReceiptMetadataNoLegacyBackfillAndImmutableEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	executionSQL(t, ctx, f, "UPDATE notification_delivery_intents SET attempt_count=1 WHERE id=$1", f.deliveries[0])
	service := executionService(t, f.pool)
	lease, err := service.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	reservation, err := service.Reserve(ctx, lease, f.deliveries[0])
	require.ErrorIs(t, err, dispatch.ErrExecutionHeld)
	require.Nil(t, reservation)
	require.Zero(t, receiptMetadataCount(t, ctx, f))
	require.Equal(t, 1, executionAttempts(t, ctx, f))
	reservation, err = service.Reserve(ctx, lease, f.deliveries[1])
	require.NoError(t, err)
	frozen, ok := reservation.ReceiptMaterial()
	require.True(t, ok)
	for _, query := range []string{"UPDATE notification_receipt_metadata SET key_id='other'", "UPDATE notification_receipt_metadata SET expires_unix=expires_unix+1", "DELETE FROM notification_receipt_metadata"} {
		_, err = f.pool.Exec(ctx, query)
		require.ErrorContains(t, err, "receipt metadata is immutable")
	}
	down, err := os.ReadFile("../../db/migrations/000029_create_receipt_metadata.down.sql")
	require.NoError(t, err)
	schema := strings.Split(f.pool.Config().ConnConfig.RuntimeParams["search_path"], ",")[0]
	_, err = f.pool.Exec(ctx, strings.ReplaceAll(string(down), "public.", pgx.Identifier{schema}.Sanitize()+"."))
	require.ErrorContains(t, err, "cannot remove retained receipt metadata")
	require.Equal(t, 1, receiptMetadataCount(t, ctx, f))
	require.NoError(t, service.Release(ctx, lease))
	// Corruptly extending an accepted deadline cannot reissue a shorter frozen scope.
	executionSQL(t, ctx, f, "UPDATE notifications SET deadline_at=clock_timestamp()+interval '2 days'; UPDATE notification_delivery_intents SET next_attempt_at=clock_timestamp()-interval '1 second'")
	lease, err = service.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	reservation, err = service.Reserve(ctx, lease, f.deliveries[1])
	require.ErrorIs(t, err, dispatch.ErrExecutionHeld)
	require.Nil(t, reservation)
	var expires int64
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT expires_unix FROM notification_receipt_metadata").Scan(&expires))
	require.Equal(t, frozen.Scope.ExpiresUnix, expires)
}

func TestReceiptMetadataScopedForeignKeyAndBoundedFields(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	var other int64
	require.NoError(t, f.pool.QueryRow(ctx, "INSERT INTO applications(key,name) VALUES('receipt-other','Other') RETURNING id").Scan(&other))
	insert := func(app int64, delivery pgtype.UUID, key, environment string) error {
		_, err := f.pool.Exec(ctx, "INSERT INTO notification_receipt_metadata(application_id,delivery_id,version,key_id,environment,expires_unix) VALUES($1,$2,1,$3,$4,2000000000)", app, delivery, key, environment)
		return err
	}
	require.ErrorContains(t, insert(other, f.deliveries[0], "key", "fixture"), "foreign key")
	require.Error(t, insert(f.app, f.deliveries[0], "key\n", "fixture"))
	require.Error(t, insert(f.app, f.deliveries[0], "key", "fixture\n"))
	require.Zero(t, receiptMetadataCount(t, ctx, f))
	executionSQL(t, ctx, f, "UPDATE notifications SET deadline_at=TIMESTAMPTZ '9999-12-31 23:59:59+00'")
	service := executionService(t, f.pool)
	lease, err := service.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	reservation, err := service.Reserve(ctx, lease, f.deliveries[0])
	require.ErrorIs(t, err, dispatch.ErrExecutionHeld)
	require.Nil(t, reservation)
	require.Zero(t, receiptMetadataCount(t, ctx, f))
	require.Zero(t, executionAttempts(t, ctx, f))
}
