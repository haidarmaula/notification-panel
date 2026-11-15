//go:build ignore

// Local-dev-setup provisions only the synthetic database in the isolated local stack.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"hello/internal/installation"
)

const dsn = "postgresql://integration:integration_only@postgres:5432/notification_integration?sslmode=verify-full&connect_timeout=3&sslrootcert=/etc/hello/ca.crt"
const topic = "notification.delivery.updated"
const sendTopic = "notification.send.requested"
const cluster = "MkU3OEVBNTcwNTJENDM2Qk"
const namespace = "load-fixture"
const concurrency = 4
const onboardingApplicationID = "10000000-0000-4000-8000-000000000002"
const verifierPoolLock int64 = 728194021

const localVerifierSlots = 3

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func verify(ok bool, message string) {
	if !ok {
		panic(message)
	}
}

type experiment struct {
	db             *pgxpool.Pool
	ctx            context.Context
	app, staff     int64
	public, source string
	devices, rate  int
}

func (e *experiment) exec(query string, args ...any) {
	_, err := e.db.Exec(e.ctx, query, args...)
	check(err)
}

const fixtureCohort = "7a000000-0000-4000-8000-000000000001"

func (e *experiment) fixtureLogin(name string) *pgx.Conn {
	cfg := e.db.Config().ConnConfig.Copy()
	cfg.User, cfg.Password = name, "fixture-only"
	conn, err := pgx.ConnectConfig(e.ctx, cfg)
	check(err)
	return conn
}

// Approve each synthetic binding through the real operator routine before the
// timed campaign. Batches bound the number of per-binding decisions in one tx.
func (e *experiment) approveBindings() {
	issuer := e.fixtureLogin("load_binding_reviewer")
	defer issuer.Close(e.ctx)
	operator := e.fixtureLogin("load_binding_executor")
	defer operator.Close(e.ctx)
	var origin, source pgtype.UUID
	check(e.db.QueryRow(e.ctx, "SELECT origin_epoch,source_epoch FROM notification_runtime WHERE singleton").Scan(&origin, &source))
	rows, err := e.db.Query(e.ctx, "SELECT id,gen_random_uuid() FROM installation_bindings WHERE application_id=$1 AND retired_at IS NULL AND NOT EXISTS(SELECT FROM current_binding_routes c WHERE c.application_id=installation_bindings.application_id AND c.binding_id=installation_bindings.id) ORDER BY id", e.app)
	check(err)
	defer rows.Close()
	bindings := make([]int64, 0, 250)
	grants := make([]pgtype.UUID, 0, 250)
	apply := func() {
		if len(grants) == 0 {
			return
		}
		tag, err := issuer.Exec(e.ctx, `INSERT INTO binding_route_grants(id,application_id,binding_id,operator_role,origin_epoch,source_epoch,
            expected_revision,destination,cohort_id,evidence_id,reason,expires_at)
            SELECT grant_id,$3,binding,'load_binding_executor'::regrole,$4,$5,0,'FCM_ACTIVE',$6,gen_random_uuid(),'pilot',clock_timestamp()+interval '1 hour'
            FROM unnest($1::uuid[],$2::bigint[]) AS page(grant_id,binding)`, grants, bindings, e.app, origin, source, fixtureCohort)
		check(err)
		verify(tag.RowsAffected() == int64(len(grants)), "missing synthetic route approvals")
		var applied int
		check(operator.QueryRow(e.ctx, `SELECT count(*) FILTER (WHERE apply_binding_route(id,$2)=1) FROM unnest($1::uuid[]) AS page(id)`, grants, e.public).Scan(&applied))
		verify(applied == len(grants), "missing applied route decisions")
		grants, bindings = grants[:0], bindings[:0]
	}
	for rows.Next() {
		var binding int64
		var id pgtype.UUID
		check(rows.Scan(&binding, &id))
		bindings, grants = append(bindings, binding), append(grants, id)
		if len(grants) == cap(grants) {
			apply()
		}
	}
	check(rows.Err())
	apply()
}

// Release the synthetic cohort and then the full audience using separate
// grants. Evidence digests represent only this isolated experiment.
func (e *experiment) releaseDispatch() {
	issuer := e.fixtureLogin("load_dispatch_reviewer")
	defer issuer.Close(e.ctx)
	operator := e.fixtureLogin("load_dispatch_executor")
	defer operator.Close(e.ctx)
	for _, transition := range []struct{ from, to, reason string }{
		{"HELD", "PILOT", "pilot"},
		{"PILOT", "FULL", "full_coverage"},
	} {
		var id, origin, source pgtype.UUID
		var appRevision, policyRevision, applied int64
		var mode string
		check(e.db.QueryRow(e.ctx, `SELECT gen_random_uuid(),r.origin_epoch,r.source_epoch,a.revision,p.dispatch_revision,p.dispatch_mode
			FROM applications a JOIN application_dispatch_policy p ON p.application_id=a.id
			CROSS JOIN notification_runtime r WHERE a.id=$1 AND r.singleton`, e.app).
			Scan(&id, &origin, &source, &appRevision, &policyRevision, &mode))
		if mode == transition.to || mode == "FULL" {
			continue
		}
		verify(mode == transition.from, "unexpected dispatch mode before release")
		_, err := issuer.Exec(e.ctx, `INSERT INTO application_dispatch_grants
			(id,project_id,operator_role,application_revision,expected_revision,previous_mode,destination,cohort_id,
			 origin_epoch,source_epoch,reason,legacy_evidence,consent_evidence,coverage_evidence,quota_evidence,expires_at)
			VALUES ($1,$2,'load_dispatch_executor'::regrole,$3,$4,$5,$6,$7,$8,$9,$10,
			    sha256('synthetic legacy fence'),sha256('synthetic consent'),sha256('synthetic coverage'),
			    sha256('synthetic quota'),clock_timestamp()+interval '1 hour')`,
			id, e.public, appRevision, policyRevision, mode, transition.to, fixtureCohort, origin, source, transition.reason)
		check(err)
		check(operator.QueryRow(e.ctx, "SELECT apply_application_dispatch($1,$2)", id, e.public).Scan(&applied))
		verify(applied == policyRevision+1, "dispatch grant did not advance the revision")
	}
}

// Seed the initial audience/account and completion marker in one transaction.
// Route/dispatch approvals follow after commit and are independently resumable.
func (e *experiment) seed(password string, beforeCommit func()) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	check(err)
	tx, err := e.db.Begin(e.ctx)
	check(err)
	defer tx.Rollback(e.ctx)
	check(tx.QueryRow(e.ctx, "SELECT id,public_id::text FROM applications WHERE key='load-fixture' AND status='ACTIVE'").Scan(&e.app, &e.public))
	check(tx.QueryRow(e.ctx, "INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Local Developer','developer@hello.local',$1 FROM roles WHERE name='SUPER_ADMIN' RETURNING id", string(hash)).Scan(&e.staff))
	execute := func(query string, args ...any) { _, err := tx.Exec(e.ctx, query, args...); check(err) }
	execute("INSERT INTO application_staff_grants(application_id,staff_id) VALUES($1,$2)", e.app, e.staff)
	execute("INSERT INTO users(application_id,external_id,name) SELECT $1,'load-'||n,'Local device '||n FROM generate_series(1,$2::int) n", e.app, e.devices)
	execute("INSERT INTO mobile_installations(application_id,owner_user_id,proof_verifier) SELECT application_id,id,decode(repeat('11',32),'hex') FROM users WHERE application_id=$1", e.app)
	protector, err := installation.NewProtector(1, map[int32][]byte{1: bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32))
	check(err)
	rows, err := tx.Query(e.ctx, "SELECT id,owner_user_id,nextval('installation_bindings_id_seq') FROM mobile_installations WHERE application_id=$1 ORDER BY id", e.app)
	check(err)
	batch := make([][]any, 0, e.devices)
	for rows.Next() {
		var inst, user, binding int64
		check(rows.Scan(&inst, &user, &binding))
		address, err := protector.Protect(installation.BindingIdentity{ApplicationID: e.app, InstallationID: inst, BindingID: binding}, namespace, installation.FCMRegistrationToken, "load-"+strconv.Itoa(len(batch)+1))
		check(err)
		batch = append(batch, []any{binding, e.app, inst, user, int64(1), "ANDROID", namespace, "route", installation.FCMRegistrationToken, address.Fingerprint[:], address.Ciphertext, int32(1)})
	}
	check(rows.Err())
	rows.Close()
	n, err := tx.CopyFrom(e.ctx, pgx.Identifier{"installation_bindings"}, []string{"id", "application_id", "installation_id", "owner_user_id", "owner_epoch", "platform", "provider_namespace", "provider_route_key", "address_type", "address_fingerprint", "address_ciphertext", "encryption_key_version"}, pgx.CopyFromRows(batch))
	check(err)
	verify(n == int64(e.devices), "missing local synthetic bindings")
	execute("UPDATE applications SET name='Hello Local' WHERE id=$1", e.app)
	execute("INSERT INTO local_dev_seed VALUES(true,$1)", e.staff)
	if beforeCommit != nil {
		beforeCommit()
	}
	check(tx.Commit(e.ctx))
}

func (e *experiment) seedOnboardingDemo() {
	e.exec(`INSERT INTO applications(public_id,key,name)
        VALUES($1::uuid,'local-onboarding','Hello Local Onboarding') ON CONFLICT(public_id) DO NOTHING`, onboardingApplicationID)
	e.exec(`INSERT INTO application_dispatch_policy(application_id,push_ttl_seconds,key_ttl_seconds)
        SELECT id,3600,86400 FROM applications WHERE public_id=$1::uuid
        ON CONFLICT(application_id) DO NOTHING`, onboardingApplicationID)
	e.exec(`INSERT INTO application_onboarding_verifier_scopes(role_id,application_id)
        SELECT 'local_onboarding_verifier'::regrole::oid,id FROM applications WHERE public_id=$1::uuid
        ON CONFLICT(role_id) DO UPDATE SET application_id=EXCLUDED.application_id`, onboardingApplicationID)
	var status string
	var revision int64
	var greenfield bool
	var scopeMatches bool
	var dispatchHeld bool
	err := e.db.QueryRow(e.ctx, `SELECT a.status,a.revision,o.greenfield,
        EXISTS(SELECT FROM application_onboarding_verifier_scopes s
          WHERE s.role_id='local_onboarding_verifier'::regrole::oid AND s.application_id=a.id),
        EXISTS(SELECT FROM application_dispatch_policy p WHERE p.application_id=a.id
          AND p.dispatch_mode='HELD' AND p.hold_new AND NOT p.coverage_approved)
        FROM applications a JOIN application_onboarding o ON o.application_id=a.id
        WHERE a.public_id=$1::uuid`, onboardingApplicationID).Scan(&status, &revision, &greenfield, &scopeMatches, &dispatchHeld)
	check(err)
	verify(status == "PROVISIONING" && revision >= 1 && greenfield && scopeMatches && dispatchHeld,
		"local onboarding demo is not a scoped provisioning application with held dispatch")
}

func runVerifierPool(ctx context.Context, db *pgxpool.Pool) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		reconcileCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := reconcileVerifierPool(reconcileCtx, db)
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("local verifier pool reconciliation failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func reconcileVerifierPool(ctx context.Context, db *pgxpool.Pool) error {
	for slot := 1; slot <= localVerifierSlots; slot++ {
		roleName := fmt.Sprintf("local_onboarding_verifier_%d", slot)
		tx, err := db.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin verifier slot reconciliation: %w", err)
		}
		appID, err := verifierSlotApplication(ctx, tx, roleName)
		if err == pgx.ErrNoRows {
			appID, err = allocateVerifierSlot(ctx, tx, roleName)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("resolve verifier slot %d: %w", slot, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit verifier slot %d: %w", slot, err)
		}
		if appID != "" {
			if err := writeVerifierPolicy(slot, appID); err != nil {
				return fmt.Errorf("write verifier slot %d policy: %w", slot, err)
			}
		}
	}
	return nil
}

func verifierSlotApplication(ctx context.Context, tx pgx.Tx, roleName string) (string, error) {
	var appID string
	err := tx.QueryRow(ctx, `SELECT a.public_id::text
        FROM application_onboarding_verifier_scopes s
        JOIN applications a ON a.id=s.application_id
        WHERE s.role_id=$1::regrole::oid`, roleName).Scan(&appID)
	return appID, err
}

func allocateVerifierSlot(ctx context.Context, tx pgx.Tx, roleName string) (string, error) {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", verifierPoolLock); err != nil {
		return "", err
	}
	// Recheck after acquiring the allocator lock in case another local pool won.
	if appID, err := verifierSlotApplication(ctx, tx, roleName); err == nil {
		return appID, nil
	} else if err != pgx.ErrNoRows {
		return "", err
	}
	var applicationID int64
	var publicID string
	err := tx.QueryRow(ctx, `SELECT a.id,a.public_id::text
        FROM applications a
        JOIN application_onboarding o ON o.application_id=a.id
        LEFT JOIN application_dispatch_policy p ON p.application_id=a.id
        WHERE a.status='PROVISIONING' AND o.greenfield AND o.environment='staging'
          AND o.candidate_configuration_id IS NOT NULL AND o.current_configuration_id IS NULL
          AND (p.application_id IS NULL OR (p.dispatch_mode='HELD' AND p.hold_new AND NOT p.coverage_approved))
          AND NOT EXISTS (SELECT FROM application_onboarding_verifier_scopes s WHERE s.application_id=a.id)
          AND NOT EXISTS (SELECT FROM application_onboarding_verifier_scopes s WHERE s.role_id=$1::regrole::oid)
        ORDER BY a.id
        FOR UPDATE OF a,o SKIP LOCKED LIMIT 1`, roleName).Scan(&applicationID, &publicID)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO application_onboarding_verifier_scopes(role_id,application_id)
        VALUES($1::regrole::oid,$2)`, roleName, applicationID); err != nil {
		return "", err
	}
	return publicID, nil
}

func writeVerifierPolicy(slot int, applicationID string) error {
	roleName := fmt.Sprintf("local_onboarding_verifier_%d", slot)
	policy := map[string]any{
		"application_id":   applicationID,
		"environment":      "staging",
		"credential_root":  "/var/lib/hello/credentials/" + applicationID + "/staging",
		"database_url":     fmt.Sprintf("postgresql://%s:fixture-only@postgres:5432/notification_integration?sslmode=verify-full&sslrootcert=/etc/hello/postgres-ca.crt&connect_timeout=3", roleName),
		"poll_interval_ms": 1000,
	}
	contents, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	path := fmt.Sprintf("/local-config/generated/controlplane-slot-%d.json", slot)
	if previous, err := os.ReadFile(path); err == nil && bytes.Equal(previous, contents) {
		return nil
	}
	temporary := path + ".new"
	if err := os.WriteFile(temporary, contents, 0600); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0600); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	check(err)
	return string(b)
}

func (e *experiment) policy() {
	p := map[string]any{
		"mode": "delivery_only", "database_url": dsn,
		"log_source_id": e.source, "cluster_identity": cluster, "topic_name": topic,
		"topic_incarnation": "load-fixture", "partition_count": 1, "retry_min_ms": 100, "retry_max_ms": 1000,
		"kafka": map[string]any{
			"brokers": []string{"kafka:9096"}, "authentication": "MTLS", "ca_pem": readFile("/local-tls/ca.crt"),
			"client_certificate_pem": readFile("/local-tls/client.crt"), "client_key_pem": readFile("/local-tls/client.key"),
			"sasl_username": "", "sasl_password": "",
		},
		"delivery": map[string]any{
			"application_id": e.public, "environment": "fixture", "project_id": "integration-fixture",
			"routes":                []map[string]any{{"namespace": namespace, "key": "route", "platform": "ANDROID", "address_type": installation.FCMRegistrationToken}},
			"credential_project_id": "integration-fixture", "service_account_email": "fixture@integration-fixture.iam.gserviceaccount.com",
			"service_account_file": "/etc/hello/service-account.json", "cross_project_approved": false,
			"lease_seconds": 60, "renew_seconds": 15, "request_timeout_seconds": 10, "retry_floor_seconds": 10, "max_attempts": 3,
			"receipt_key_id": "fixture-key", "observation_window_seconds": 86400, "replica_count": 1,
			"requests_per_second": e.rate, "project_requests_per_second": e.rate, "concurrency": concurrency,
			"breaker_threshold": 5, "breaker_cooldown_seconds": 10, "poll_interval_ms": 100, "page_size": 100,
			"current_key_version": 1, "encryption_keys": []map[string]any{{"version": 1, "key": bytes.Repeat([]byte{1}, 32)}},
			"fingerprint_key": bytes.Repeat([]byte{2}, 32),
			"signing_keys":    []map[string]any{{"id": "fixture-key", "key": ed25519.NewKeyFromSeed(bytes.Repeat([]byte{11}, 32))}},
		},
	}
	data, err := json.Marshal(p)
	check(err)
	check(os.WriteFile("/etc/hello/worker.json", data, 0640))
}

func writeJSON(name string, value any) {
	raw, err := json.MarshalIndent(value, "", "  ")
	check(err)
	check(os.WriteFile("/local-config/generated/"+name, raw, 0600))
}
func main() {
	if len(os.Args) == 2 && os.Args[1] == "gateway" {
		gateway()
		return
	}
	testSeed := len(os.Args) == 2 && os.Args[1] == "--test-seed-recovery"
	verifierPool := len(os.Args) == 2 && os.Args[1] == "verifier-pool"
	if len(os.Args) != 1 && !testSeed && !verifierPool {
		log.Fatal("invalid local setup command")
	}
	if verifierPool {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		db, err := pgxpool.New(ctx, dsn)
		check(err)
		defer db.Close()
		runVerifierPool(ctx, db)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	connection := dsn
	if testSeed {
		connection = strings.Replace(dsn, "/notification_integration?", "/hello_local_seed_test?", 1)
	}
	db, err := pgxpool.New(ctx, connection)
	check(err)
	defer db.Close()
	e := experiment{db: db, ctx: ctx, devices: 3, rate: 20}
	var settings struct {
		Password string `json:"password"`
	}
	check(json.Unmarshal([]byte(readFile("/local-config/settings.json")), &settings))
	if len(settings.Password) < 16 {
		log.Fatal("local configuration missing")
	}
	if testSeed {
		e.exec("CREATE TABLE local_dev_seed(singleton boolean PRIMARY KEY CHECK(singleton),staff_id bigint NOT NULL)")
		failed := false
		func() {
			defer func() {
				if recover() != nil {
					failed = true
				}
			}()
			e.seed(settings.Password, func() { panic("simulated setup interruption") })
		}()
		verify(failed, "seed interruption did not occur")
		var rows int
		check(db.QueryRow(ctx, "SELECT (SELECT count(*) FROM staff_users)+(SELECT count(*) FROM users)+(SELECT count(*) FROM installation_bindings)+(SELECT count(*) FROM local_dev_seed)").Scan(&rows))
		verify(rows == 0, "interrupted seed leaked partial data")
		e.seed(settings.Password, nil)
		check(db.QueryRow(ctx, "SELECT count(*) FROM local_dev_seed").Scan(&rows))
		verify(rows == 1, "seed retry did not complete")
		log.Print("interrupted initial seed rolled back and retry completed without duplicate data")
		return
	}
	var seeded bool
	e.exec("CREATE TABLE IF NOT EXISTS local_dev_seed(singleton boolean PRIMARY KEY CHECK(singleton),staff_id bigint NOT NULL)")
	e.exec(`INSERT INTO local_dev_seed SELECT true,s.id FROM staff_users s
 JOIN application_staff_grants g ON g.staff_id=s.id JOIN applications a ON a.id=g.application_id
 WHERE s.email='developer@hello.local' AND a.key='load-fixture'
 ON CONFLICT(singleton) DO NOTHING`)
	check(db.QueryRow(ctx, "SELECT EXISTS(SELECT FROM local_dev_seed)").Scan(&seeded))
	if !seeded {
		e.seed(settings.Password, nil)
	} else {
		check(db.QueryRow(ctx, "SELECT id,public_id::text FROM applications WHERE key='load-fixture' AND status='ACTIVE'").Scan(&e.app, &e.public))
	}
	e.exec(`UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='SUPER_ADMIN')
        WHERE email='developer@hello.local' AND role_id<>(SELECT id FROM roles WHERE name='SUPER_ADMIN')`)
	e.seedOnboardingDemo()
	e.exec("UPDATE notification_runtime SET sealed=false,migration_hold=false")
	e.approveBindings()
	e.releaseDispatch()
	_, err = db.Exec(ctx, `INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation,active)
 SELECT gen_random_uuid(),$1,$2,'load-fixture',true WHERE NOT EXISTS(SELECT FROM notification_log_sources WHERE topic=$1 AND active)`, topic, cluster)
	check(err)
	check(db.QueryRow(ctx, "SELECT id::text FROM notification_log_sources WHERE topic=$1 AND active", topic).Scan(&e.source))

	e.policy()
	var delivery map[string]any
	check(json.Unmarshal([]byte(readFile("/etc/hello/worker.json")), &delivery))
	writeJSON("delivery.json", delivery)
	var sendSource string
	_, err = db.Exec(ctx, `INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation,active)
 SELECT gen_random_uuid(),$1,$2,'load-fixture',true WHERE NOT EXISTS(SELECT FROM notification_log_sources WHERE topic=$1 AND active)`, sendTopic, cluster)
	check(err)
	check(db.QueryRow(ctx, "SELECT id::text FROM notification_log_sources WHERE topic=$1 AND active", sendTopic).Scan(&sendSource))
	delete(delivery, "delivery")
	delivery["log_source_id"] = sendSource
	delivery["topic_name"] = sendTopic
	delivery["mode"] = "admission_only"
	delivery["group_id"] = "hello-local-admission-v1"
	writeJSON("admission.json", delivery)
	delete(delivery, "mode")
	delete(delivery, "group_id")
	delivery["poll_interval_ms"] = 1000
	delivery["page_size"] = 100
	writeJSON("scheduler.json", delivery)
	writeJSON("application.json", map[string]any{"id": e.public, "onboarding_id": onboardingApplicationID})
	log.Print("local application and daemon policies ready; no notification sent")
}
func gateway() {
	roots := x509.NewCertPool()
	verify(roots.AppendCertsFromPEM([]byte(readFile("/local-tls/ca.crt"))), "invalid local CA")
	destination, err := url.Parse("http://server:8080")
	check(err)
	proxy := httputil.NewSingleHostReverseProxy(destination)
	server := http.Server{Addr: ":8082", Handler: proxy, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}}
	check(server.ListenAndServeTLS("/local-tls/server.crt", "/local-tls/server.key"))
}
