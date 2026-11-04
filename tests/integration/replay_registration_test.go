//go:build integration

package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
)

type replayRegistrationFixture struct {
	replayFixture
	service *dispatch.ReplayRequests
	key     ed25519.PrivateKey
	project pgtype.UUID
}

func newReplayRegistrationFixture(t *testing.T, ctx context.Context) replayRegistrationFixture {
	t.Helper()
	f := replayRegistrationFixture{replayFixture: newReplayFixture(t, ctx)}
	_, err := f.pool.Exec(ctx, `ALTER TABLE applications DROP CONSTRAINT applications_status_check;
 UPDATE applications SET status='ACTIVE'; UPDATE notification_runtime SET sealed=false; UPDATE notification_log_sources SET active=true`)
	require.NoError(t, err)
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT public_id FROM applications WHERE id=$1", f.app).Scan(&f.project))
	_, err = f.pool.Exec(ctx, "INSERT INTO application_staff_grants(application_id,staff_id) VALUES($1,$2)", f.app, f.staff)
	require.NoError(t, err)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	f.key = private
	verifier, err := dispatch.NewReplayVerifier("fixture-operator", "fixture-replay", "fixture-deployment", public)
	require.NoError(t, err)
	f.service, err = dispatch.NewReplayRequests(f.pool, verifier, f.source)
	require.NoError(t, err)
	return f
}

func (f replayRegistrationFixture) credential(t *testing.T, ctx context.Context, request dispatch.ReplayRequest) (string, pgtype.UUID) {
	t.Helper()
	grant := replayUUID(t, ctx, f.pool)
	hash, err := request.Fingerprint(f.staff, grant)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `INSERT INTO notification_replay_grants(id,application_id,staff_user_id,request_id,issuer,deployment,selection_hash,expires_at)
 VALUES($1,$2,$3,$4,'fixture-operator','fixture-deployment',$5,clock_timestamp()+interval '3 minutes')`, grant, f.app, f.staff, request.ID, hash[:])
	require.NoError(t, err)
	now := time.Now()
	claims := jwt.MapClaims{"iss": "fixture-operator", "aud": "fixture-replay", "sub": strconv.FormatInt(f.staff, 10), "jti": "fixture-credential", "iat": now.Add(-10 * time.Second).Unix(), "exp": now.Add(2 * time.Minute).Unix(), "auth_time": now.Add(-20 * time.Second).Unix(), "amr": []string{"mfa"}, "purpose": "notification_replay", "deployment": "fixture-deployment", "application_id": request.ApplicationID.String(), "request_id": request.ID.String(), "grant_id": grant.String(), "selection_hash": base64.RawURLEncoding.EncodeToString(hash[:])}
	credential, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(f.key)
	require.NoError(t, err)
	return credential, grant
}

func (f replayRegistrationFixture) input(t *testing.T, ctx context.Context) dispatch.ReplayRequest {
	t.Helper()
	return dispatch.ReplayRequest{ApplicationID: f.project, ID: replayUUID(t, ctx, f.pool), Positions: []dispatch.ReplayPosition{{LogSourceID: f.source, Partition: 0, Offset: 42}}, ChangeReference: "INC-123"}
}

func TestReplayRegistrationAuthorityAndAudit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := newReplayRegistrationFixture(t, ctx)
	request := f.input(t, ctx)
	credential, _ := f.credential(t, ctx, request)
	registered, err := f.service.Register(ctx, request, credential)
	require.NoError(t, err)
	require.False(t, registered.AlreadyRegistered)
	require.Equal(t, request.ID, registered.RequestID)
	registered, err = f.service.Register(ctx, request, credential)
	require.NoError(t, err)
	require.True(t, registered.AlreadyRegistered)
	var count int
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM audit_logs WHERE action='NOTIFICATION_REPLAY_REQUEST' AND actor_user_id=$1 AND application_id=$2", f.staff, f.app).Scan(&count))
	require.Equal(t, 1, count, "identical retry must not append another request audit")
	var after string
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT after_json::text FROM audit_logs WHERE action='NOTIFICATION_REPLAY_REQUEST'").Scan(&after))
	require.NotContains(t, after, credential)
	require.NotContains(t, after, "selection_hash")
	require.Contains(t, after, "INC-123")
	request.ChangeReference = "INC-456"
	changed, _ := f.credential(t, ctx, request)
	result, err := f.service.Register(ctx, request, changed)
	require.ErrorIs(t, err, dispatch.ErrReplayConflict)
	require.Equal(t, dispatch.ReplayRegistration{}, result)

	// Auditing is part of the registration transaction, not a best-effort side effect.
	_, err = f.pool.Exec(ctx, "ALTER TABLE audit_logs ADD CONSTRAINT reject_replay_audit CHECK (action<>'NOTIFICATION_REPLAY_REQUEST') NOT VALID")
	require.NoError(t, err)
	next := f.input(t, ctx)
	nextCredential, _ := f.credential(t, ctx, next)
	result, err = f.service.Register(ctx, next, nextCredential)
	require.Error(t, err)
	require.Equal(t, dispatch.ReplayRegistration{}, result)
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_replay_requests WHERE id=$1", next.ID).Scan(&count))
	require.Zero(t, count, "audit failure rolls back the request header")
}

func TestReplayRegistrationRechecksCurrentAuthority(t *testing.T) {
	for name, change := range map[string]string{
		"staff inactive":              "UPDATE staff_users SET is_active=false",
		"operator cannot replay":      "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='OPERATOR')",
		"revoked app grant":           "UPDATE application_staff_grants SET is_active=false",
		"super admin has no wildcard": "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='SUPER_ADMIN'); DELETE FROM application_staff_grants",
		"revoked replay grant":        "UPDATE notification_replay_grants SET revoked_at=clock_timestamp()",
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			f := newReplayRegistrationFixture(t, ctx)
			request := f.input(t, ctx)
			credential, _ := f.credential(t, ctx, request)
			_, err := f.pool.Exec(ctx, change)
			require.NoError(t, err)
			result, err := f.service.Register(ctx, request, credential)
			require.ErrorIs(t, err, dispatch.ErrReplayDenied)
			require.Equal(t, dispatch.ReplayRegistration{}, result)
			var count int
			require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_replay_requests").Scan(&count))
			require.Zero(t, count, "denied requests must not reserve request identities")
		})
	}
}

func TestReplayRegistrationGrantRevocationSerializes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f := newReplayRegistrationFixture(t, ctx)
	request := f.input(t, ctx)
	credential, grant := f.credential(t, ctx, request)
	tx, err := f.pool.BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	_, err = tx.Exec(ctx, "UPDATE notification_replay_grants SET revoked_at=clock_timestamp() WHERE id=$1", grant)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { _, err := f.service.Register(ctx, request, credential); done <- err }()
	// Observe the actual PostgreSQL lock wait before releasing revocation.
	require.Eventually(t, func() bool {
		var waiting bool
		err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
            WHERE datname=current_database() AND pid<>pg_backend_pid()
            AND wait_event_type='Lock' AND query LIKE '%FROM notification_replay_grants%')`).Scan(&waiting)
		return err == nil && waiting
	}, 2*time.Second, 10*time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("registration passed an uncommitted grant revocation: %v", err)
	default:
	}
	require.NoError(t, tx.Commit(ctx))
	require.ErrorIs(t, <-done, dispatch.ErrReplayDenied)
	_, err = f.pool.Exec(ctx, "UPDATE notification_replay_grants SET revoked_at=NULL WHERE id=$1", grant)
	require.Error(t, err, "revocation cannot be undone in place")
}

// A real commit followed by an error models loss of the commit acknowledgement.
type replayUncertainCommit struct{ *pgxpool.Pool }
type replayUncertainTx struct{ pgx.Tx }

func (s replayUncertainCommit) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	tx, err := s.Pool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return replayUncertainTx{Tx: tx}, nil
}
func (tx replayUncertainTx) Commit(ctx context.Context) error {
	if err := tx.Tx.Commit(ctx); err != nil {
		return err
	}
	return errors.New("fixture commit acknowledgement lost")
}

func TestReplayRegistrationUncertainCommitAndReauthorization(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f := newReplayRegistrationFixture(t, ctx)
	request := f.input(t, ctx)
	credential, grant := f.credential(t, ctx, request)
	verifier, err := dispatch.NewReplayVerifier("fixture-operator", "fixture-replay", "fixture-deployment", f.key.Public().(ed25519.PublicKey))
	require.NoError(t, err)
	uncertain, err := dispatch.NewReplayRequests(replayUncertainCommit{Pool: f.pool}, verifier, f.source)
	require.NoError(t, err)
	result, err := uncertain.Register(ctx, request, credential)
	require.Error(t, err)
	require.Equal(t, dispatch.ReplayRegistration{}, result, "unknown commit must not report success")
	result, err = f.service.Register(ctx, request, credential)
	require.NoError(t, err)
	require.True(t, result.AlreadyRegistered, "same request reconciles a commit that actually persisted")
	_, err = f.pool.Exec(ctx, "UPDATE notification_replay_grants SET revoked_at=clock_timestamp() WHERE id=$1", grant)
	require.NoError(t, err)
	result, err = f.service.Register(ctx, request, credential)
	require.ErrorIs(t, err, dispatch.ErrReplayDenied, "even a committed duplicate must reauthorize")
	require.Equal(t, dispatch.ReplayRegistration{}, result)
	var count int
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM audit_logs WHERE action='NOTIFICATION_REPLAY_REQUEST'").Scan(&count))
	require.Equal(t, 1, count)
}

func TestReplayRegistrationConcurrentIdenticalRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f := newReplayRegistrationFixture(t, ctx)
	request := f.input(t, ctx)
	credential, _ := f.credential(t, ctx, request)
	type response struct {
		result dispatch.ReplayRegistration
		err    error
	}
	done := make(chan response, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			result, err := f.service.Register(ctx, request, credential)
			done <- response{result: result, err: err}
		}()
	}
	close(start)
	duplicates := 0
	for range 2 {
		r := <-done
		require.NoError(t, r.err)
		require.Equal(t, request.ID, r.result.RequestID)
		if r.result.AlreadyRegistered {
			duplicates++
		}
	}
	require.Equal(t, 1, duplicates)
	var count int
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM audit_logs WHERE action='NOTIFICATION_REPLAY_REQUEST'").Scan(&count))
	require.Equal(t, 1, count)
}

func TestReplayRegistrationCancellationReleasesConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f := newReplayRegistrationFixture(t, ctx)
	request := f.input(t, ctx)
	credential, _ := f.credential(t, ctx, request)
	blocker, err := f.pool.BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		_ = blocker.Rollback(cleanup)
	}()
	_, err = blocker.Exec(ctx, "SELECT id FROM applications WHERE id=$1 FOR UPDATE", f.app)
	require.NoError(t, err)
	short, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	result, err := f.service.Register(short, request, credential)
	stop()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, dispatch.ReplayRegistration{}, result)
	require.Eventually(t, func() bool { return f.pool.Stat().AcquiredConns() == 1 }, time.Second, 10*time.Millisecond)
	require.NoError(t, blocker.Rollback(ctx))
	result, err = f.service.Register(ctx, request, credential)
	require.NoError(t, err)
	require.False(t, result.AlreadyRegistered, "canceled registration left no partial header")
}

func TestReplayRegistrationHeldAppGrantsNoExecution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f := newReplayRegistrationFixture(t, ctx)
	request := f.input(t, ctx)
	credential, _ := f.credential(t, ctx, request)
	_, err := f.pool.Exec(ctx, "UPDATE applications SET status='HELD'")
	require.NoError(t, err)
	result, err := f.service.Register(ctx, request, credential)
	require.NoError(t, err, "metadata registration must permit later no-op reconciliation during app holds")
	require.False(t, result.AlreadyRegistered)
	for _, table := range []string{"notification_replay_attempts", "notification_replay_controls", "notification_delivery_intents"} {
		var n int
		require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n))
		require.Zero(t, n, "a held app registration granted no execution")
	}
}
