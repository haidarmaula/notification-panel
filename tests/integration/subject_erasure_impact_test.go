//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hello/internal/installation"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

type erasurePreview struct {
	Selection    string `json:"selection"`
	DispatchHold bool   `json:"dispatch_hold_new"`
	HeldClasses  int    `json:"held_retention_classes"`
	Groups       map[string]struct {
		Coverage string         `json:"coverage"`
		Counts   map[string]int `json:"counts"`
	} `json:"groups"`
	Unknown map[string]*int `json:"unknown_counts"`
}

func TestSubjectErasureImpactScopesAndBounds(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	exec := func(s string, args ...any) { t.Helper(); executionSQL(t, ctx, f, s, args...) }
	const namespace, subject = "case-fixture:mobile", "private-subject ' \\ \n :untrusted"
	exec("INSERT INTO mobile_identities VALUES($1,$2,$3,$4),($1,'separate-issuer','private-alias',$4)", f.app, namespace, subject, f.user)
	exec("UPDATE users SET email='private-person@example.invalid' WHERE id=$1", f.user)
	var otherProject pgtype.UUID
	var otherApp int64
	require.NoError(t, f.pool.QueryRow(ctx, "INSERT INTO applications(key,name) VALUES('erase-other','Other') RETURNING id,public_id").Scan(&otherApp, &otherProject))
	exec(`WITH outsider AS (INSERT INTO users(application_id,external_id) VALUES($1,'private-other-person') RETURNING id)
		INSERT INTO mobile_identities SELECT $1,$2,$3,id FROM outsider`, otherApp, namespace, subject)
	exec(`WITH other AS (INSERT INTO users(application_id,external_id) VALUES($1,'private-another-issuer') RETURNING id)
		INSERT INTO mobile_identities SELECT $1,'third-issuer',$2,id FROM other`, f.app, subject)
	worker := executionService(t, f.pool)
	lease, err := worker.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	_, err = worker.Reserve(ctx, lease, f.deliveries[0])
	require.NoError(t, err)
	exec(`UPDATE notification_delivery_intents SET attempt_count=2,last_result_disposition='unconfirmed',last_result_reason='unknown',
		last_result_fingerprint=decode(repeat('11',32),'hex') WHERE id=$1`, f.deliveries[0])
	exec(`UPDATE notification_delivery_intents SET status='OPENED',attempt_count=1,attempt_id=gen_random_uuid(),claim_generation=1,
		last_attempt_at=clock_timestamp(),attempt_resolved_at=clock_timestamp(),provider_message_id='private-provider-result',
		provider_accepted_at=clock_timestamp() WHERE id=$1`, f.deliveries[1])
	exec("INSERT INTO notification_reads(application_id,user_id,notification_id) VALUES($1,$2,$3)", f.app, f.user, f.campaign)
	exec("INSERT INTO notification_observations(application_id,delivery_id,event,received_at) VALUES($1,$2,'OPENED',clock_timestamp())", f.app, f.deliveries[1])
	exec("INSERT INTO notification_receipt_requests SELECT $1,$2,gen_random_uuid(),$3,decode(repeat('22',32),'hex'),expires_unix FROM notification_receipt_metadata WHERE delivery_id=$3", f.app, f.user, f.deliveries[0])
	exec(`INSERT INTO notification_update_outbox(event_id,application_id,notification_id,delivery_id,delivery_version,origin_epoch,source_epoch,payload,occurred_at)
		SELECT gen_random_uuid(),$1,$2,$3,1,origin_epoch,source_epoch,'private-event-payload',clock_timestamp() FROM notification_runtime`, f.app, f.campaign, f.deliveries[1])
	exec(`INSERT INTO notification_send_outbox(application_id,notification_id,requested_at,deadline_at,origin_epoch,source_epoch)
		SELECT application_id,id,queued_at,deadline_at,origin_epoch,source_epoch FROM notifications WHERE id=$1`, f.campaign)
	exec("INSERT INTO device_tokens(application_id,user_id,provider,platform,push_token) VALUES($1,$2,'FCM','ANDROID','private-legacy-address')", f.app, f.user)
	exec("INSERT INTO retention_policies(application_id,record_class,policy_version,retain_after_expiry_seconds,held) VALUES($1,'receipt_request',gen_random_uuid(),0,true)", f.app)
	exec("UPDATE application_dispatch_policy SET hold_new=true WHERE application_id=$1", f.app)
	f.evidence(t, ctx, 80)
	f.attempt(t, ctx, f.request(t, ctx), 80)
	_, err = f.lifecycle.Apply(ctx, installation.Principal{ApplicationID: f.app, UserID: f.user}, installation.Change{Operation: installation.Refresh,
		InstallationID: f.enrollment.State.PublicID, ExpectedVersion: f.enrollment.State.Version, Proof: f.enrollment.Proof,
		Route: installation.Route{Namespace: "execution-fixture", Key: "route", Platform: "ANDROID", AddressType: installation.FCMRegistrationToken}, Address: "private-modern-address"})
	require.NoError(t, err)

	schema := strings.Split(f.pool.Config().ConnConfig.RuntimeParams["search_path"], ",")[0]
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	role := fmt.Sprintf("privacy_inventory_%d", time.Now().UnixNano())
	login := pgx.Identifier{role}.Sanitize()
	exec("CREATE ROLE " + login + " LOGIN PASSWORD 'inventory-fixture'")
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := f.pool.Exec(cleanup, "DROP OWNED BY "+login+"; DROP ROLE "+login)
		require.NoError(t, err)
	})
	source := func(path string) string {
		t.Helper()
		b, err := os.ReadFile("../../scripts/sql/" + path)
		require.NoError(t, err)
		return strings.ReplaceAll(string(b), "public.", quotedSchema+".")
	}
	grants := strings.ReplaceAll(source("subject-erasure-reader-grants.sql"), "SCHEMA public", "SCHEMA "+quotedSchema)
	exec(strings.ReplaceAll(grants, `:"preview_reader"`, login))
	cfg := f.pool.Config()
	cfg.ConnConfig.User, cfg.ConnConfig.Password = role, "inventory-fixture"
	reader, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(reader.Close)
	for _, bad := range []string{"SELECT proof_verifier FROM mobile_installations", "SELECT address_ciphertext FROM installation_bindings",
		"SELECT push_token FROM device_tokens", "SELECT provider_message_id FROM notification_delivery_intents",
		"SELECT lease_owner FROM notifications", "SELECT attempt_id FROM notification_delivery_intents", "SELECT active_claim_token FROM notification_replay_controls",
		"SELECT original_payload FROM notification_send_dead_letters", "SELECT payload FROM notification_update_outbox", "SELECT email FROM users",
		"UPDATE users SET status='INACTIVE'"} {
		_, err := reader.Exec(ctx, bad)
		requireRetentionDenied(t, err)
	}
	query := source("subject-erasure-impact.sql")
	run := func(selection string) erasurePreview {
		t.Helper()
		tx, err := reader.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		require.NoError(t, err)
		defer tx.Rollback(ctx)
		_, err = tx.Exec(ctx, "SET LOCAL statement_timeout='3s'; SET LOCAL lock_timeout='250ms'")
		require.NoError(t, err)
		var result []byte
		require.NoError(t, tx.QueryRow(ctx, query, selection).Scan(&result))
		require.NotContains(t, string(result), "private-")
		require.NotContains(t, string(result), namespace)
		require.NotContains(t, string(result), f.enrollment.Proof)
		var p erasurePreview
		require.NoError(t, json.Unmarshal(result, &p))
		return p
	}
	selectFor := func(project pgtype.UUID, ns string) string {
		t.Helper()
		b, err := json.Marshal(map[string]string{"application_id": project.String(), "identity_namespace": ns, "subject": subject})
		require.NoError(t, err)
		return string(b)
	}
	primary := selectFor(f.project, namespace)
	report := run(primary)
	checkErasurePreviewPSQL(t, ctx, role, query, primary, report)
	uppercase := strings.Replace(primary, f.project.String(), strings.ToUpper(f.project.String()), 1)
	require.Equal(t, report, run(uppercase))
	require.Equal(t, "matched", report.Selection)
	require.True(t, report.DispatchHold)
	require.Equal(t, 1, report.HeldClasses)
	for group, counts := range map[string]map[string]int{
		"identity_aliases": {"mappings": 2}, "current_installations": {"owned": 2}, "binding_generations": {"current": 2, "retired": 1},
		"legacy_registrations": {"scoped": 1}, "delivery_intents": {"total": 2, "pending": 1, "ever_charged": 2, "unresolved_charge": 1, "last_result_unconfirmed": 1, "provider_acknowledged": 1},
		"campaign_reads": {"rows": 1}, "receipt_requests": {"dedup_rows": 1}, "frozen_capabilities": {"unexpired": 1}, "receipt_observations": {"opened": 1},
		"linked_campaigns": {"total": 1, "live_leases": 1}, "unpublished_delivery_updates": {"events": 1}, "unpublished_campaign_sends": {"events": 1},
		"attributed_replay_attempts": {"total": 1, "started": 1, "without_position_resolution": 1},
	} {
		require.Equal(t, "exact", report.Groups[group].Coverage, group)
		for name, want := range counts {
			require.Contains(t, report.Groups[group].Counts, name)
			require.Equal(t, want, report.Groups[group].Counts[name], group+"."+name)
		}
	}
	for _, external := range []string{"unattributed_quarantine", "provider_retained_copies", "published_kafka_records", "consumer_projections", "archive_copies", "recoverable_backup_copies", "subject_legal_holds"} {
		require.Contains(t, report.Unknown, external)
		require.Nil(t, report.Unknown[external])
	}
	for _, sel := range []string{selectFor(otherProject, namespace), selectFor(f.project, "third-issuer")} {
		other := run(sel)
		require.Equal(t, "matched", other.Selection)
		require.Equal(t, 1, other.Groups["identity_aliases"].Counts["mappings"])
		require.Zero(t, other.Groups["delivery_intents"].Counts["total"])
	}
	for _, bad := range []string{"{}", selectFor(f.project, "unknown"), `{"subject":"a","subject":"b"}`, "not-json",
		strings.Replace(primary, `"application_id":`, `"extra":true,"application_id":`, 1), primary + strings.Repeat(" ", 4096)} {
		missing := run(bad)
		require.Equal(t, "unresolved", missing.Selection)
		require.Nil(t, missing.Groups)
	}
	exec("INSERT INTO mobile_identities SELECT $1,'bounded','private-'||i::text,$2 FROM generate_series(1,502) i", f.app, f.user)
	exec(`INSERT INTO notification_targets(application_id,notification_id,target_type,user_id)
        SELECT $1,$2,'INDIVIDUAL',$3 FROM generate_series(1,502)`, f.app, f.campaign, f.user)
	large := run(primary)
	require.Equal(t, "lower_bound", large.Groups["identity_aliases"].Coverage)
	require.Equal(t, 501, large.Groups["identity_aliases"].Counts["mappings"])
	require.Equal(t, "lower_bound", large.Groups["individual_targets"].Coverage)
	require.Equal(t, "lower_bound", large.Groups["linked_campaigns"].Coverage)
	require.Equal(t, "lower_bound", large.Groups["attributed_replay_attempts"].Coverage)
	require.Equal(t, "exact", large.Groups["delivery_intents"].Coverage)
}

func checkErasurePreviewPSQL(t *testing.T, ctx context.Context, role, query, selection string, want erasurePreview) {
	t.Helper()
	fixtureDir := os.Getenv("HELLO_INTEGRATION_CERTS_DIR")
	require.NotEmpty(t, fixtureDir)
	dir, err := os.MkdirTemp(fixtureDir, "erasure-preview-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
	wrapper, err := os.ReadFile("../../scripts/sql/preview-subject-erasure.psql")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "preview-subject-erasure.psql"), wrapper, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subject-erasure-impact.sql"), []byte(query), 0644))
	path := "/fixtures/" + filepath.Base(dir) + "/preview-subject-erasure.psql"
	dsn := "host=127.0.0.1 dbname=notification_integration sslmode=verify-full sslrootcert=/fixtures/ca.crt user=" + role

	for _, tc := range []struct {
		name, input string
		want        erasurePreview
	}{
		{name: "exact quoted identity", input: selection, want: want},
		{name: "duplicate field", input: `{"subject":"private-first","subject":"private-second"}`, want: erasurePreview{Selection: "unresolved"}},
		{name: "untrusted command text", input: `\! private-command`, want: erasurePreview{Selection: "unresolved"}},
		{name: "oversize", input: strings.Repeat(" ", 4096) + selection, want: erasurePreview{Selection: "unresolved"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.CommandContext(ctx, "docker", "compose", "--env-file", "/dev/null", "-f", "../../compose.integration.yml", "-p", "hello-integration",
				"exec", "-T", "-e", "PGPASSWORD=inventory-fixture", "postgres", "psql", "-XqAtw", "-v", "ON_ERROR_STOP=1", "-f", path, dsn)
			cmd.Stdin = strings.NewReader(tc.input + "\n")
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
			require.NotContains(t, string(output), "private-")
			var report erasurePreview
			require.NoError(t, json.Unmarshal(output, &report))
			require.Equal(t, tc.want, report)
		})
	}
}
