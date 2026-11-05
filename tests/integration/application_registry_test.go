//go:build integration

package integration

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/audit"
	"hello/internal/database"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

func TestApplicationRegistryPersistence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(context.Background())
	q := sqlc.New(tx)
	repo := repository.NewApplicationRepository(q)
	seedStaff := func(role string) int64 {
		var id int64
		require.NoError(t, tx.QueryRow(ctx, `INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Registry fixture',gen_random_uuid()::text || '@fixture.invalid','fixture-only' FROM roles WHERE name=$1 RETURNING id`, role).Scan(&id))
		return id
	}
	actor, target := seedStaff("SUPER_ADMIN"), seedStaff("VIEWER")
	a, err := repo.Create(ctx, "mobile-a", "Aplikasi A")
	require.NoError(t, err)
	b, err := repo.Create(ctx, "mobile-b", "Aplikasi B")
	require.NoError(t, err)
	require.Positive(t, a.ID)
	require.True(t, a.PublicID.Valid)
	require.NotEqual(t, a.PublicID, b.PublicID)
	require.Equal(t, "PROVISIONING", a.Status)
	require.Equal(t, int64(1), a.Revision)
	found, err := repo.FindByPublicID(ctx, a.PublicID)
	require.NoError(t, err)
	require.Equal(t, a, found)
	locked, err := repo.LockByPublicID(ctx, a.PublicID)
	require.NoError(t, err)
	require.Equal(t, a, locked)
	page, err := repo.ListManaged(ctx, "", 0, 20)
	require.NoError(t, err)
	require.Equal(t, int64(2), page.Total)
	require.Equal(t, applicationRowsUTC([]sqlc.Application{a, b}), applicationRowsUTC(page.Items))
	page, err = repo.ListManaged(ctx, "mobile-a", 100, 20)
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	require.NotNil(t, page.Items)
	require.Empty(t, page.Items)
	page, err = repo.ListManaged(ctx, "unknown", 0, 20)
	require.NoError(t, err)
	require.Zero(t, page.Total)
	require.Empty(t, page.Items)
	renamed, err := repo.Rename(ctx, a.ID, 1, "Aplikasi A baru")
	require.NoError(t, err)
	require.Equal(t, int64(2), renamed.Revision)
	require.Equal(t, a.Key, renamed.Key)
	require.Equal(t, a.PublicID, renamed.PublicID)
	_, err = repo.Rename(ctx, a.ID, 1, "stale")
	require.ErrorIs(t, err, pgx.ErrNoRows)
	_, err = repo.Rename(ctx, a.ID, 2, renamed.Name)
	require.ErrorIs(t, err, pgx.ErrNoRows)
	found, err = repo.FindByPublicID(ctx, a.PublicID)
	require.NoError(t, err)
	require.Equal(t, renamed, found)
	g, err := repo.CreateGrant(ctx, a.ID, actor)
	require.NoError(t, err)
	require.True(t, g.IsActive)
	require.Equal(t, int64(1), g.Revision)
	_, err = repo.CreateGrant(ctx, b.ID, target)
	require.NoError(t, err)
	granted, err := repo.ListGranted(ctx, actor, 0, 20)
	require.NoError(t, err)
	require.Equal(t, int64(1), granted.Total)
	require.Equal(t, applicationRowsUTC([]sqlc.Application{renamed}), applicationRowsUTC(granted.Items))
	granted, err = repo.ListGranted(ctx, actor, 100, 20)
	require.NoError(t, err)
	require.Equal(t, int64(1), granted.Total)
	require.Empty(t, granted.Items)
	auth, err := repo.GetAuthority(ctx, actor, a.PublicID)
	require.NoError(t, err)
	require.Equal(t, "SUPER_ADMIN", auth.RoleName.String)
	require.True(t, auth.StaffIsActive)
	require.True(t, auth.GrantIsActive.Valid)
	require.True(t, auth.GrantIsActive.Bool)
	missingGrant, err := repo.GetAuthority(ctx, actor, b.PublicID)
	require.NoError(t, err)
	require.True(t, missingGrant.ApplicationID.Valid)
	require.False(t, missingGrant.GrantIsActive.Valid)
	unknown := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	missingApp, err := repo.GetAuthority(ctx, actor, unknown)
	require.NoError(t, err)
	require.Equal(t, actor, missingApp.StaffID)
	require.False(t, missingApp.ApplicationID.Valid)
	_, err = repo.GetAuthority(ctx, 999999, unknown)
	require.ErrorIs(t, err, pgx.ErrNoRows)
	_, err = tx.Exec(ctx, "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='VIEWER'),is_active=false WHERE id=$1", actor)
	require.NoError(t, err)
	auth, err = repo.GetAuthority(ctx, actor, a.PublicID)
	require.NoError(t, err)
	require.False(t, auth.StaffIsActive)
	require.Equal(t, "VIEWER", auth.RoleName.String)
	_, err = tx.Exec(ctx, "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='SUPER_ADMIN'),is_active=true WHERE id=$1", actor)
	require.NoError(t, err)
	revoked, err := repo.SetGrantActive(ctx, a.ID, actor, 1, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), revoked.Revision)
	granted, err = repo.ListGranted(ctx, actor, 0, 20)
	require.NoError(t, err)
	require.Zero(t, granted.Total)
	require.Empty(t, granted.Items)
	grants, err := repo.ListGrants(ctx, a.ID, 100, 20)
	require.NoError(t, err)
	require.Equal(t, int64(1), grants.Total)
	require.Empty(t, grants.Items)
	grants, err = repo.ListGrants(ctx, a.ID, 0, 20)
	require.NoError(t, err)
	require.Equal(t, grantRowsUTC([]sqlc.ApplicationStaffGrant{revoked}), grantRowsUTC(grants.Items))
	regranted, err := repo.SetGrantActive(ctx, a.ID, actor, 2, true)
	require.NoError(t, err)
	require.Equal(t, int64(3), regranted.Revision)
	_, err = repo.SetGrantActive(ctx, a.ID, actor, 1, false)
	require.ErrorIs(t, err, pgx.ErrNoRows)
	_, err = repo.SetGrantActive(ctx, a.ID, actor, 3, true)
	require.ErrorIs(t, err, pgx.ErrNoRows)
	currentGrant, err := repo.GetGrant(ctx, a.ID, actor)
	require.NoError(t, err)
	require.Equal(t, regranted, currentGrant)
	expectCode := func(code string, fn func() error) {
		_, err := tx.Exec(ctx, "SAVEPOINT constraint_case")
		require.NoError(t, err)
		err = fn()
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr)
		require.Equal(t, code, pgErr.Code)
		_, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT constraint_case")
		require.NoError(t, err)
		_, err = tx.Exec(ctx, "RELEASE SAVEPOINT constraint_case")
		require.NoError(t, err)
	}
	for _, tc := range []struct{ name, key, value, code string }{
		{"duplicate", "mobile-a", "Duplicate", "23505"},
		{"bad key", "Bad_Key", "Name", "23514"},
		{"long key", strings.Repeat("a", 64), "Name", "22001"},
		{"empty name", "empty-name", "", "23514"},
		{"padded name", "padded-name", " Name ", "23514"},
		{"newline name", "newline-name", "Name\nforged", "23514"},
		{"unicode whitespace", "unicode-space", "\u2003", "23514"},
		{"byte budget", "byte-budget", strings.Repeat("é", 65), "23514"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectCode(tc.code, func() error { _, err := repo.Create(ctx, tc.key, tc.value); return err })
		})
	}
	_, err = repo.Create(ctx, "unicode-boundary", strings.Repeat("é", 64))
	require.NoError(t, err)
	for _, status := range []string{"ACTIVE", "HELD", "RETIRED"} {
		expectCode("23514", func() error {
			_, err := tx.Exec(ctx, "UPDATE applications SET status=$1 WHERE id=$2", status, a.ID)
			return err
		})
	}
	expectCode("23514", func() error {
		_, err := tx.Exec(ctx, "UPDATE applications SET revision=0 WHERE id=$1", a.ID)
		return err
	})
	expectCode("23514", func() error {
		_, err := tx.Exec(ctx, "INSERT INTO applications(id,key,name) VALUES(0,'zero-app','Zero')")
		return err
	})
	expectCode("23505", func() error {
		_, err := tx.Exec(ctx, "INSERT INTO applications(public_id,key,name) VALUES($1,'copy-app','Copy')", a.PublicID)
		return err
	})
	expectCode("23505", func() error { _, err := repo.CreateGrant(ctx, a.ID, actor); return err })
	expectCode("23514", func() error { _, err := repo.CreateGrant(ctx, a.ID, 0); return err })
	expectCode("23503", func() error { _, err := repo.CreateGrant(ctx, a.ID, 999999); return err })
	expectCode("23503", func() error { _, err := repo.CreateGrant(ctx, 999999, actor); return err })
	expectCode("23514", func() error {
		_, err := tx.Exec(ctx, "UPDATE application_staff_grants SET revision=0 WHERE application_id=$1", a.ID)
		return err
	})
	expectCode("23503", func() error { _, err := tx.Exec(ctx, "DELETE FROM applications WHERE id=$1", a.ID); return err })
	auditCtx := context.WithValue(ctx, middleware.UserContextKey, &token.AccessClaims{StaffID: actor})
	auditRepo := repository.NewAuditLogRepository(q)
	auditService := audit.NewAuditService(auditRepo)
	require.NoError(t, auditService.Log(auditCtx, audit.LogParams{ApplicationID: a.ID, Action: audit.ACTION_APPLICATION_CREATE, EntityType: audit.ENTITY_TYPE_APPLICATION, EntityID: a.ID, EntityName: a.Name}))
	require.NoError(t, auditService.Log(auditCtx, audit.LogParams{ApplicationID: b.ID, Action: audit.ACTION_APPLICATION_GRANT_ENABLE, EntityType: audit.ENTITY_TYPE_APPLICATION_GRANT, EntityID: target, EntityName: b.Name}))
	require.NoError(t, auditService.Log(auditCtx, audit.LogParams{Action: audit.ACTION_STAFF_UPDATE_PASSWORD, EntityType: audit.ENTITY_TYPE_STAFF, EntityID: actor}))
	audits, err := auditRepo.List(ctx, 0, 20)
	require.NoError(t, err)
	require.Len(t, audits, 3)
	var scoped, global int
	for _, v := range audits {
		row, err := auditRepo.FindByID(ctx, v.ID)
		require.NoError(t, err)
		require.Equal(t, v.ApplicationID, row.ApplicationID)
		if row.ApplicationID.Valid {
			scoped++
		} else {
			global++
		}
	}
	require.Equal(t, 2, scoped)
	require.Equal(t, 1, global)
	expectCode("23514", func() error {
		return auditService.Log(auditCtx, audit.LogParams{Action: audit.ACTION_APPLICATION_CREATE, EntityType: audit.ENTITY_TYPE_APPLICATION})
	})
	expectCode("23503", func() error {
		return auditService.Log(auditCtx, audit.LogParams{ApplicationID: 999999, Action: audit.ACTION_APPLICATION_CREATE, EntityType: audit.ENTITY_TYPE_APPLICATION})
	})
	_, err = tx.Exec(ctx, "DELETE FROM staff_users WHERE id=$1", target)
	require.NoError(t, err)
	_, err = repo.GetGrant(ctx, b.ID, target)
	require.ErrorIs(t, err, pgx.ErrNoRows)
	var retained int
	require.NoError(t, tx.QueryRow(ctx, "SELECT count(*) FROM audit_logs WHERE application_id=$1 AND entity_id=$2", b.ID, target).Scan(&retained))
	require.Equal(t, 1, retained)
	expectCode("23503", func() error { _, err := tx.Exec(ctx, "DELETE FROM applications WHERE id=$1", b.ID); return err })
	// Caller-owned transaction plumbing, not an implemented service guarantee.
	child, err := tx.Begin(ctx)
	require.NoError(t, err)
	childRepo := repository.NewApplicationRepository(sqlc.New(child))
	rolled, err := childRepo.Create(ctx, "rollback-app", "Rollback")
	require.NoError(t, err)
	_, err = childRepo.CreateGrant(ctx, rolled.ID, actor)
	require.NoError(t, err)
	err = audit.NewAuditService(repository.NewAuditLogRepository(sqlc.New(child))).Log(auditCtx, audit.LogParams{EntityType: audit.ENTITY_TYPE_APPLICATION, Action: audit.ACTION_APPLICATION_CREATE})
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "23514", pgErr.Code)
	require.NoError(t, child.Rollback(ctx))
	_, err = repo.FindByPublicID(ctx, rolled.PublicID)
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestApplicationRegistryLockAndCAS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	repo := repository.NewApplicationRepository(sqlc.New(pool))
	app, err := repo.Create(ctx, "concurrent-app", "Concurrent")
	require.NoError(t, err)
	defer func() {
		_, err := pool.Exec(context.Background(), "DELETE FROM applications WHERE id=$1", app.ID)
		require.NoError(t, err)
	}()
	first, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer first.Rollback(context.Background())
	_, err = repository.NewApplicationRepository(sqlc.New(first)).LockByPublicID(ctx, app.PublicID)
	require.NoError(t, err)
	second, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer second.Rollback(context.Background())
	_, err = second.Exec(ctx, "SET LOCAL lock_timeout='100ms'")
	require.NoError(t, err)
	_, err = repository.NewApplicationRepository(sqlc.New(second)).LockByPublicID(ctx, app.PublicID)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "55P03", pgErr.Code)
	require.NoError(t, second.Rollback(ctx))
	require.NoError(t, first.Rollback(ctx))
	results := make(chan error, 2)
	for _, name := range []string{"Concurrent A", "Concurrent B"} {
		go func(name string) { _, err := repo.Rename(ctx, app.ID, 1, name); results <- err }(name)
	}
	var success, stale int
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			success++
		} else {
			require.True(t, errors.Is(err, pgx.ErrNoRows))
			stale++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, stale)
	final, err := repo.FindByPublicID(ctx, app.PublicID)
	require.NoError(t, err)
	require.Equal(t, int64(2), final.Revision)
}

// Binary pgx timestamps and JSON timestamps can use different time.Location
// representations. Compare their instants and all other fields, not zone internals.
func applicationRowsUTC(rows []sqlc.Application) []sqlc.Application {
	out := append([]sqlc.Application(nil), rows...)
	for i := range out {
		out[i].CreatedAt.Time = out[i].CreatedAt.Time.UTC()
		out[i].UpdatedAt.Time = out[i].UpdatedAt.Time.UTC()
	}
	return out
}
func grantRowsUTC(rows []sqlc.ApplicationStaffGrant) []sqlc.ApplicationStaffGrant {
	out := append([]sqlc.ApplicationStaffGrant(nil), rows...)
	for i := range out {
		out[i].CreatedAt.Time = out[i].CreatedAt.Time.UTC()
		out[i].UpdatedAt.Time = out[i].UpdatedAt.Time.UTC()
	}
	return out
}

// Exercises the actual paired SQL on a transaction-isolated fixture, including
// an old global audit row and a legacy entity-label collision that must fail closed.
func TestApplicationRegistryMigrationRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(context.Background())
	executionDown, err := os.ReadFile("../../db/migrations/000028_create_execution_claims.down.sql")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(executionDown))
	require.NoError(t, err)
	dispositionDown, err := os.ReadFile("../../db/migrations/000027_add_replay_resolved_disposition.down.sql")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(dispositionDown))
	require.NoError(t, err)
	grantDown, err := os.ReadFile("../../db/migrations/000026_create_replay_operational_grants.down.sql")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(grantDown))
	require.NoError(t, err)
	replayDown, err := os.ReadFile("../../db/migrations/000025_create_replay_persistence.down.sql")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(replayDown))
	require.NoError(t, err)
	quarantineDown, err := os.ReadFile("../../db/migrations/000024_create_send_quarantine.down.sql")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(quarantineDown))
	require.NoError(t, err)
	preparationDown, err := os.ReadFile("../../db/migrations/000023_create_durable_preparation.down.sql")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(preparationDown))
	require.NoError(t, err)
	acceptanceDown, err := os.ReadFile("../../db/migrations/000022_create_durable_acceptance.down.sql")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(acceptanceDown))
	require.NoError(t, err)
	originDown, err := os.ReadFile("../../db/migrations/000021_create_mobile_origin_uses.down.sql")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(originDown))
	require.NoError(t, err)
	identityDown, err := os.ReadFile("../../db/migrations/000020_create_mobile_identities.down.sql")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(identityDown))
	require.NoError(t, err)
	bindingDown, err := os.ReadFile("../../db/migrations/000018_create_installation_bindings.down.sql")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(bindingDown))
	require.NoError(t, err)
	// Roll back dependent expansion first, inside this rollback-only fixture.
	expansionDown, err := os.ReadFile("../../db/migrations/000017_expand_business_application_scope.down.sql")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(expansionDown))
	require.NoError(t, err)
	up, err := os.ReadFile("../../db/migrations/000016_create_application_registry.up.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("../../db/migrations/000016_create_application_registry.down.sql")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(down))
	require.NoError(t, err)
	var actor int64
	require.NoError(t, tx.QueryRow(ctx, `INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Migration fixture',gen_random_uuid()::text || '@fixture.invalid','fixture-only' FROM roles WHERE name='SUPER_ADMIN' RETURNING id`).Scan(&actor))
	var auditID int64
	require.NoError(t, tx.QueryRow(ctx, `INSERT INTO audit_logs(actor_user_id,action,entity_type) VALUES($1,'FIXTURE_GLOBAL','fixture_global') RETURNING id`, actor).Scan(&auditID))
	var before string
	require.NoError(t, tx.QueryRow(ctx, "SELECT to_jsonb(a)::text FROM audit_logs a WHERE id=$1", auditID).Scan(&before))
	_, err = tx.Exec(ctx, string(up))
	require.NoError(t, err)
	var after string
	var scope pgtype.Int8
	require.NoError(t, tx.QueryRow(ctx, "SELECT (to_jsonb(a)-'application_id')::text,application_id FROM audit_logs a WHERE id=$1", auditID).Scan(&after, &scope))
	require.JSONEq(t, before, after)
	require.False(t, scope.Valid)
	_, err = repository.NewApplicationRepository(sqlc.New(tx)).Create(ctx, "round-trip", "Round trip")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(down))
	require.NoError(t, err)
	_, err = tx.Exec(ctx, "SAVEPOINT collision")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_user_id,action,entity_type) VALUES($1,'LEGACY_LABEL','application')`, actor)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(up))
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "23514", pgErr.Code)
	_, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT collision")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(up))
	require.NoError(t, err)
	var count int64
	require.NoError(t, tx.QueryRow(ctx, "SELECT count(*) FROM applications").Scan(&count))
	require.Zero(t, count)
}
