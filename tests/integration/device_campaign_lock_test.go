//go:build integration

package integration

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

type pauseDeviceCommit struct {
	*pgxpool.Pool
	next             atomic.Bool
	reached, release chan struct{}
}

func (p *pauseDeviceCommit) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	tx, err := p.Pool.BeginTx(ctx, opts)
	if err != nil || !p.next.Swap(false) {
		return tx, err
	}
	return &pausedDeviceTx{Tx: tx, barrier: p}, nil
}

type pausedDeviceTx struct {
	pgx.Tx
	barrier *pauseDeviceCommit
}

func (p *pausedDeviceTx) Commit(ctx context.Context) error {
	close(p.barrier.reached)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.barrier.release:
		return p.Tx.Commit(ctx)
	}
}

func TestIndependentDevicesShareCampaignAndFenceMutation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	f := readySubmission(t, ctx)
	db := &pauseDeviceCommit{Pool: f.pool, reached: make(chan struct{}), release: make(chan struct{})}
	service := executionService(t, db)
	lease, err := service.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	db.next.Store(true)
	first := make(chan error, 1)
	go func() { _, err := service.Reserve(ctx, lease, f.deliveries[0]); first <- err }()
	defer func() { close(db.release); require.NoError(t, <-first) }()
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-db.reached:
	}
	// The campaign cannot be changed while that first reservation holds it.
	_, err = f.pool.Exec(ctx, `SELECT id FROM notifications WHERE id=$1 FOR NO KEY UPDATE NOWAIT`, f.campaign)
	var lock *pgconn.PgError
	require.ErrorAs(t, err, &lock)
	require.Equal(t, "55P03", lock.Code)
	// Every stage on the independent device can finish before the first device
	// commits, including final provider authorization and the durable result.
	c := submissionClient(t, f, submissionAccess)
	result, err := submissionSender(t, service, c).Send(ctx, lease, f.deliveries[1])
	require.NoError(t, err)
	_, err = service.RecordResult(ctx, result)
	require.NoError(t, err)
}
