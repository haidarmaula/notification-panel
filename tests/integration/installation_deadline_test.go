//go:build integration

package integration

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"hello/internal/installation"
)

func TestInstallationDeadlineReleasesBlockedTransaction(t *testing.T) {
	setup, cancelSetup := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelSetup()
	pool := acceptanceFixture(t, setup)
	_, err := pool.Exec(setup, "ALTER TABLE applications DROP CONSTRAINT applications_status_check")
	require.NoError(t, err)
	var applicationID, userID int64
	require.NoError(t, pool.QueryRow(setup, `INSERT INTO applications(key,name,status)
		VALUES('installation-deadline','Deadline fixture','ACTIVE') RETURNING id`).Scan(&applicationID))
	require.NoError(t, pool.QueryRow(setup, `INSERT INTO users(application_id,external_id)
		VALUES($1,'installation-deadline-user') RETURNING id`, applicationID).Scan(&userID))
	protector, err := installation.NewProtector(1, map[int32][]byte{1: bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32))
	require.NoError(t, err)
	lifecycle, err := installation.NewLifecycle(pool, protector)
	require.NoError(t, err)

	// Hold the project lock on a different connection while lifecycle tries to enroll.
	blocker, err := pool.Begin(setup)
	require.NoError(t, err)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = blocker.Rollback(cleanup)
	}()
	_, err = blocker.Exec(setup, "SELECT id FROM applications WHERE id=$1 FOR UPDATE", applicationID)
	require.NoError(t, err)
	actor := installation.Principal{ApplicationID: applicationID, UserID: userID}
	route := installation.Route{
		Namespace: "deadline-project", Key: "deadline-route",
		Platform: "ANDROID", AddressType: installation.FCMRegistrationToken,
	}
	caller, cancelCaller := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelCaller()
	started := time.Now()
	result, err := lifecycle.Enroll(caller, actor, route, "deadline-address")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Empty(t, result.Proof)
	require.Less(t, time.Since(started), 8*time.Second)
	require.NoError(t, caller.Err(), "the lifecycle budget must expire before the caller budget")
	require.Eventually(t, func() bool {
		return pool.Stat().AcquiredConns() == 1 // Only the blocker still owns a connection.
	}, time.Second, 10*time.Millisecond)
	var installations, bindings int64
	require.NoError(t, pool.QueryRow(caller, "SELECT count(*) FROM mobile_installations").Scan(&installations))
	require.NoError(t, pool.QueryRow(caller, "SELECT count(*) FROM installation_bindings").Scan(&bindings))
	require.Zero(t, installations)
	require.Zero(t, bindings)
	require.NoError(t, blocker.Rollback(caller))

	// The pool and address remain usable after the blocked operation was canceled.
	result, err = lifecycle.Enroll(caller, actor, route, "deadline-address")
	require.NoError(t, err)
	require.NotEmpty(t, result.Proof)
}
