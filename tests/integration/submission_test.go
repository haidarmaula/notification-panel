//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
	"hello/internal/fcm"
	"hello/internal/installation"
	"hello/internal/pushpayload"
	"hello/internal/receipt"
)

type submissionToken func(context.Context) (*auth.Token, error)

func (f submissionToken) Token(ctx context.Context) (*auth.Token, error) { return f(ctx) }
func submissionClient(t *testing.T, f executionFixture, source submissionToken) *fcm.Client {
	t.Helper()
	c, err := fcm.NewIntegrationClient(fcm.Config{ProjectID: "integration-fixture", Environment: "fixture",
		Routes:            []pushpayload.Route{{ApplicationID: f.project, Environment: "fixture", Namespace: "execution-fixture", Key: "route", Platform: "ANDROID", AddressType: installation.FCMRegistrationToken}},
		RequestsPerSecond: 1000, Concurrency: 4, RequestTimeout: 10 * time.Second, BreakerThreshold: 5, BreakerCooldown: 30 * time.Second}, source)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}
func submissionSender(t *testing.T, execution *dispatch.Execution, c *fcm.Client) *dispatch.Submitter {
	t.Helper()
	signer, err := receipt.NewSigner("fixture", map[string]ed25519.PrivateKey{"fixture-key": ed25519.NewKeyFromSeed(bytes.Repeat([]byte{11}, 32))})
	require.NoError(t, err)
	protector, err := installation.NewProtector(1, map[int32][]byte{1: bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32))
	require.NoError(t, err)
	sender, err := dispatch.NewSubmitter(execution, c, signer, protector)
	require.NoError(t, err)
	return sender
}
func submissionAccess(context.Context) (*auth.Token, error) {
	return &auth.Token{Value: "synthetic_access_token", Type: "Bearer", Expiry: time.Now().Add(time.Hour)}, nil
}
func readySubmission(t *testing.T, ctx context.Context) executionFixture {
	t.Helper()
	f := newExecutionFixture(t, ctx)
	executionSQL(t, ctx, f, `UPDATE notifications SET payload='{"fixture":"integration"}'::jsonb`)
	return f
}
func TestSubmissionCopiedReservationSendsOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := readySubmission(t, ctx)
	execution := executionService(t, f.pool)
	lease, err := execution.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	c := submissionClient(t, f, submissionAccess)
	sender := submissionSender(t, execution, c)
	first, err := c.Acquire(ctx)
	require.NoError(t, err)
	r, err := execution.Reserve(ctx, lease, f.deliveries[0])
	require.NoError(t, err)
	second, err := c.Acquire(ctx)
	require.NoError(t, err)
	copied := *r
	var wg sync.WaitGroup
	type submissionRun struct {
		value *dispatch.SubmissionResult
		err   error
	}
	results := make(chan submissionRun, 2)
	wg.Go(func() { v, e := sender.SubmitReserved(ctx, r, first); results <- submissionRun{v, e} })
	wg.Go(func() { v, e := sender.SubmitReserved(ctx, &copied, second); results <- submissionRun{v, e} })
	wg.Wait()
	close(results)
	accepted := 0
	for result := range results {
		if result.err != nil {
			require.ErrorIs(t, result.err, dispatch.ErrExecutionUnavailable)
			continue
		}
		out, ok := result.value.Outcome()
		require.True(t, ok)
		require.Equal(t, fcm.Accepted, out.Disposition)
		require.Equal(t, 1, out.PhysicalRequests)
		accepted++
	}
	require.Equal(t, 1, accepted)
	require.Equal(t, 1, executionAttempts(t, ctx, f))
	// The ordinary driver performs route/permit/reservation before submitting another device.
	result, err := sender.Send(ctx, lease, f.deliveries[1])
	require.NoError(t, err)
	out, ok := result.Outcome()
	require.True(t, ok)
	require.Equal(t, fcm.Accepted, out.Disposition)
	require.Equal(t, 2, executionAttempts(t, ctx, f))
}
func TestSubmissionRechecksAfterCredentialDelay(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"app hold", `UPDATE applications SET status='PROVISIONING'`},
		{"policy hold", `UPDATE application_dispatch_policy SET hold_new=true`},
		{"origin", `UPDATE notification_runtime SET origin_epoch=gen_random_uuid()`},
		{"source", `UPDATE notification_runtime SET source_epoch=gen_random_uuid()`},
		{"lease expiry", `UPDATE notifications SET lease_until=clock_timestamp()-interval '1 second'`},
		{"resolved attempt", `UPDATE notification_delivery_intents SET attempt_resolved_at=clock_timestamp() WHERE attempt_id IS NOT NULL`},
		{"early observation", `UPDATE notification_delivery_intents SET status='OPENED'`},
		{"user disabled", `UPDATE users SET status='INACTIVE'`},
		{"retired binding", `UPDATE installation_bindings SET retired_at=clock_timestamp()`},
		{"owner epoch", `UPDATE installation_bindings SET retired_at=clock_timestamp(); UPDATE mobile_installations SET owner_user_id=NULL,owner_epoch=owner_epoch+1,version=version+1`},
		{"payload mutation", `UPDATE notifications SET title='changed after preparation'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			f := readySubmission(t, ctx)
			execution := executionService(t, f.pool)
			lease, err := execution.Acquire(ctx, f.project, f.campaign)
			require.NoError(t, err)
			var refreshed atomic.Int32
			c := submissionClient(t, f, func(ctx context.Context) (*auth.Token, error) {
				// A separate connection must obtain these locks while credentials refresh.
				bounded, stop := context.WithTimeout(ctx, time.Second)
				defer stop()
				_, err := f.pool.Exec(bounded, tc.sql)
				require.NoError(t, err)
				refreshed.Add(1)
				return submissionAccess(ctx)
			})
			sender := submissionSender(t, execution, c)
			result, err := sender.Send(ctx, lease, f.deliveries[0])
			require.Error(t, err)
			require.EqualValues(t, 1, refreshed.Load())
			require.NotNil(t, result)
			out, ok := result.Outcome()
			require.True(t, ok)
			require.Equal(t, fcm.NotSubmitted, out.Disposition)
			require.Equal(t, fcm.AuthorizationFailure, out.Reason)
			require.Zero(t, out.PhysicalRequests)
			require.Equal(t, 1, executionAttempts(t, ctx, f))
		})
	}
}

type submissionFaultDB struct {
	*pgxpool.Pool
	count  atomic.Int32
	failAt int32
}

func (d *submissionFaultDB) BeginTx(ctx context.Context, o pgx.TxOptions) (pgx.Tx, error) {
	tx, err := d.Pool.BeginTx(ctx, o)
	if err != nil {
		return nil, err
	}
	return submissionFaultTx{Tx: tx, fail: d.count.Add(1) == d.failAt}, nil
}

type submissionFaultTx struct {
	pgx.Tx
	fail bool
}

func (tx submissionFaultTx) Commit(ctx context.Context) error {
	if err := tx.Tx.Commit(ctx); err != nil {
		return err
	}
	if tx.fail {
		return errors.New("lost validation commit acknowledgement")
	}
	return nil
}
func TestSubmissionUnknownValidationCommitNeverSends(t *testing.T) {
	for _, stage := range []struct {
		name   string
		commit int32
	}{{"prepare", 3}, {"physical check", 4}} {
		t.Run(stage.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			f := readySubmission(t, ctx)
			db := &submissionFaultDB{Pool: f.pool, failAt: stage.commit}
			execution := executionService(t, db)
			lease, err := execution.Acquire(ctx, f.project, f.campaign)
			require.NoError(t, err)
			c := submissionClient(t, f, submissionAccess)
			sender := submissionSender(t, execution, c)
			permit, err := c.Acquire(ctx)
			require.NoError(t, err)
			r, err := execution.Reserve(ctx, lease, f.deliveries[0])
			require.NoError(t, err)
			result, err := sender.SubmitReserved(ctx, r, permit)
			require.Error(t, err)
			if result != nil {
				out, ok := result.Outcome()
				require.True(t, ok)
				require.Zero(t, out.PhysicalRequests)
			}
			require.Equal(t, 1, executionAttempts(t, ctx, f))
			permit, err = c.Acquire(ctx)
			require.NoError(t, err)
			result, err = sender.SubmitReserved(ctx, r, permit)
			require.ErrorIs(t, err, dispatch.ErrExecutionUnavailable)
			require.Nil(t, result)
		})
	}
}

func TestSubmissionMissingRetainedKeysNeverReachesTransport(t *testing.T) {
	for _, kind := range []string{"signing", "decryption"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			f := readySubmission(t, ctx)
			execution := executionService(t, f.pool)
			lease, err := execution.Acquire(ctx, f.project, f.campaign)
			require.NoError(t, err)
			c := submissionClient(t, f, func(context.Context) (*auth.Token, error) {
				t.Error("missing key reached credentials")
				return nil, errors.New("unexpected")
			})
			keyID := "fixture-key"
			version := int32(1)
			if kind == "signing" {
				keyID = "new-key"
			} else {
				version = 2
			}
			signer, err := receipt.NewSigner("fixture", map[string]ed25519.PrivateKey{keyID: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{11}, 32))})
			require.NoError(t, err)
			protector, err := installation.NewProtector(version, map[int32][]byte{version: bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32))
			require.NoError(t, err)
			sender, err := dispatch.NewSubmitter(execution, c, signer, protector)
			require.NoError(t, err)
			result, err := sender.Send(ctx, lease, f.deliveries[0])
			require.ErrorIs(t, err, dispatch.ErrExecutionHeld)
			require.Nil(t, result)
			require.Equal(t, 1, executionAttempts(t, ctx, f))
		})
	}
}
