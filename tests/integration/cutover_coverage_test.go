//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"hello/internal/installation"
)

func TestCutoverCoverageScopesUsersInstallationsAndLegacyRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	route := installation.Route{Namespace: "execution-fixture", Key: "route", Platform: "IOS", AddressType: installation.FCMRegistrationToken}
	_, err := f.lifecycle.Enroll(ctx, installation.Principal{ApplicationID: f.app, UserID: f.user}, route, "secret-enrollment-fixture")
	require.NoError(t, err)
	executionSQL(t, ctx, f, `INSERT INTO users(application_id,external_id) VALUES ($1,'legacy-example'),($1,'empty-example');
        `, f.app)
	executionSQL(t, ctx, f, `INSERT INTO device_tokens(application_id,user_id,provider,platform,push_token,app_version) VALUES($1,$2,'FCM','ANDROID','secret-mixed-address','secret-version')`, f.app, f.user)
	executionSQL(t, ctx, f, `INSERT INTO device_tokens(application_id,user_id,provider,platform,push_token,app_version)
        SELECT application_id,id,'APNS','IOS','secret-legacy-address','2.5.1' FROM users WHERE application_id=$1 AND external_id='legacy-example'`, f.app)
	executionSQL(t, ctx, f, `WITH other AS (INSERT INTO applications(key,name) VALUES('coverage-other','Other') RETURNING id)
        INSERT INTO users(application_id,external_id) SELECT id,'foreign-person' FROM other`)
	raw, err := os.ReadFile("../../scripts/sql/cutover-coverage.sql")
	require.NoError(t, err)
	tx, err := f.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	query := strings.ReplaceAll(string(raw), ":'project'", "$1")
	var payload []byte
	require.NoError(t, tx.QueryRow(ctx, query, f.project).Scan(&payload))
	var got map[string]any
	require.NoError(t, json.Unmarshal(payload, &got))
	for key, want := range map[string]int{"active_users": 3, "users_with_approved_modern_only": 0, "users_with_legacy_only": 1, "users_with_both_registries": 1, "users_without_current_address": 1, "owned_installations": 3, "current_modern_bindings": 3, "approved_bindings": 2, "unreconciled_legacy_registrations": 2} {
		require.EqualValues(t, want, got[key], key)
	}
	require.Contains(t, got, "eligible_physical_devices")
	require.Nil(t, got["eligible_physical_devices"])
	require.Contains(t, got, "consent_excluded_devices")
	require.Nil(t, got["consent_excluded_devices"])
	require.Equal(t, false, got["groups_truncated"])
	require.Contains(t, string(payload), "FCM_ENROLLED")
	require.Contains(t, string(payload), "2.5.1")
	require.NotContains(t, string(payload), "secret-")
	require.NotContains(t, string(payload), "foreign-person")
	require.ErrorIs(t, tx.QueryRow(ctx, query, "00000000-0000-4000-8000-000000000099").Scan(&payload), pgx.ErrNoRows)
}
