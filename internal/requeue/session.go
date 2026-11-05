package requeue

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"hello/internal/database/sqlc"
	"hello/internal/dispatch"
	"hello/internal/kafka"
)

var errSession = errors.New("replay session unavailable")
var errDrain = errors.New("replay producer drain failed")

// Open authenticates before connecting, then checks current DB authority and the
// approved source against live broker metadata. It grants no role/source override.
// The configuration must come from a trusted deployment file, never caller input.
// The returned session is serial; its owner must stop calls and Close before
// handing off a source or epoch. Startup failure closes all acquired resources.
func (c *Configuration) Open(ctx context.Context, request dispatch.ReplayRequest, credential string, execute bool) (result Session, err error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if c == nil || c.database == nil || c.verifier == nil || !configurationEnvironmentAllowed() {
		return nil, ErrConfiguration
	}
	if _, err := c.verifier.Verify(request, credential); err != nil {
		return nil, err
	}
	for _, position := range request.Positions {
		if position.LogSourceID != c.source.id || position.Partition >= c.source.partitionCount {
			return nil, dispatch.ErrReplayDenied
		}
	}
	pool, err := pgxpool.NewWithConfig(ctx, c.database.Copy())
	if err != nil {
		return nil, safeStartupError(err)
	}
	session := &replaySession{pool: pool}
	defer func() {
		if result == nil {
			if closeErr := session.Close(); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
		}
	}()
	if err := pool.Ping(ctx); err != nil {
		return nil, safeStartupError(err)
	}
	if err := verifyRegisteredSource(ctx, pool, c.source); err != nil {
		return nil, safeStartupError(err)
	}
	session.requests, err = dispatch.NewReplayRequests(pool, c.verifier, c.source.id)
	if err != nil {
		return nil, safeStartupError(err)
	}
	// This is authorization preflight only. Every later operation rechecks current
	// authority; a successful dry run is never retained as permission to execute.
	if _, err := session.requests.Inspect(ctx, request, credential, request.Positions[0]); err != nil {
		return nil, safeStartupError(err)
	}
	session.producer, err = kafka.NewAuthenticatedScopedProducer(c.kafka)
	if err != nil {
		return nil, ErrConfiguration
	}
	if err := session.producer.VerifyClusterTopic(ctx, c.source.clusterIdentity, c.source.partitionCount); err != nil {
		return nil, safeStartupError(err)
	}
	if execute {
		session.executor, err = dispatch.NewReplayExecutor(session.requests, sourcePublisher{producer: session.producer, source: c.source})
		if err != nil {
			return nil, safeStartupError(err)
		}
	}
	return session, nil
}

type replaySession struct {
	pool       *pgxpool.Pool
	producer   *kafka.ScopedProducer
	requests   *dispatch.ReplayRequests
	executor   *dispatch.ReplayExecutor
	closeOnce  sync.Once
	closeError error
}

func (s *replaySession) Inspect(ctx context.Context, r dispatch.ReplayRequest, credential string, p dispatch.ReplayPosition) (dispatch.ReplayAssessment, error) {
	return s.requests.Inspect(ctx, r, credential, p)
}
func (s *replaySession) ReplayOne(ctx context.Context, r dispatch.ReplayRequest, credential string, p dispatch.ReplayPosition) (dispatch.ReplayAttemptResult, error) {
	if s.executor == nil {
		return dispatch.ReplayAttemptResult{}, dispatch.ErrReplayDenied
	}
	return s.executor.ReplayOne(ctx, r, credential, p)
}
func (s *replaySession) Close() error {
	s.closeOnce.Do(func() {
		// The writer owns potentially late broker I/O. Drain before releasing the DB
		// pool; never detach Close into an unjoined goroutine after a local timeout.
		if s.producer != nil && s.producer.Close() != nil {
			s.closeError = errDrain
		}
		if s.pool != nil {
			s.pool.Close()
		}
	})
	return s.closeError
}

type sourcePublisher struct {
	producer *kafka.ScopedProducer
	source   sourceManifest
}

func (p sourcePublisher) PublishScopedSend(ctx context.Context, event kafka.ScopedSendRequested) error {
	if err := p.producer.VerifyClusterTopic(ctx, p.source.clusterIdentity, p.source.partitionCount); err != nil {
		return err
	}
	return p.producer.PublishScopedSend(ctx, event)
}

func verifyRegisteredSource(ctx context.Context, pool *pgxpool.Pool, manifest sourceManifest) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := sqlc.New(tx)
	runtime, err := q.LockNotificationRuntime(ctx)
	if err != nil {
		return err
	}
	if runtime.Sealed {
		return dispatch.ErrReplayHeld
	}
	source, err := q.LockSendLogSource(ctx, manifest.id)
	if errors.Is(err, pgx.ErrNoRows) {
		return kafka.ErrScopedSource
	}
	if err != nil {
		return err
	}
	if !source.Active || source.Topic != manifest.topicName || source.ClusterIdentity != manifest.clusterIdentity || source.TopicIncarnation != manifest.topicIncarnation {
		return kafka.ErrScopedSource
	}
	return tx.Commit(ctx)
}

func safeStartupError(err error) error {
	for _, allowed := range []error{context.Canceled, context.DeadlineExceeded, dispatch.ErrReplayCredential, dispatch.ErrReplayDenied, dispatch.ErrReplayHeld, dispatch.ErrReplayConflict, kafka.ErrScopedSource} {
		if errors.Is(err, allowed) {
			return allowed
		}
	}
	return errSession
}
