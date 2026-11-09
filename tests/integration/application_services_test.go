//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"hello/internal/audit"
	"hello/internal/authz"
	"hello/internal/database"
	"hello/internal/database/sqlc"
	"hello/internal/features/applications"
	"hello/internal/middleware"
	"hello/internal/token"
)

func TestApplicationServices(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	q := sqlc.New(pool)
	service := applications.NewService(q, pool)
	ids := []int64{}
	keys := []string{}
	defer func() {
		_, err := pool.Exec(context.Background(), "DELETE FROM audit_logs WHERE actor_user_id=ANY($1)", ids)
		require.NoError(t, err)
		_, err = pool.Exec(context.Background(), "DELETE FROM application_staff_grants WHERE application_id IN(SELECT id FROM applications WHERE key=ANY($1))", keys)
		require.NoError(t, err)
		_, err = pool.Exec(context.Background(), "DELETE FROM applications WHERE key=ANY($1)", keys)
		require.NoError(t, err)
		_, err = pool.Exec(context.Background(), "DELETE FROM staff_sessions WHERE staff_user_id=ANY($1)", ids)
		require.NoError(t, err)
		_, err = pool.Exec(context.Background(), "DELETE FROM staff_users WHERE id=ANY($1)", ids)
		require.NoError(t, err)
	}()
	seed := func(role string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Service fixture',gen_random_uuid()::text || '@fixture.invalid','fixture-only' FROM roles WHERE name=$1 RETURNING id`, role).Scan(&id))
		ids = append(ids, id)
		return id
	}
	actors := map[string]int64{}
	for _, role := range []string{"VIEWER", "OPERATOR", "ADMIN", "SUPER_ADMIN"} {
		actors[role] = seed(role)
	}
	identity := func(id int64) context.Context {
		return context.WithValue(ctx, middleware.UserContextKey, &token.AccessClaims{StaffID: id, RoleName: "SUPER_ADMIN"})
	}
	super := fixtureStaffContext(t, ctx, q, actors["SUPER_ADMIN"])
	create := func(key, name string) sqlc.Application {
		keys = append(keys, key)
		v, err := service.Create(super, key, name)
		require.NoError(t, err)
		return v
	}
	a, b := create("m03-service-a", "Aplikasi A"), create("m03-service-b", "Aplikasi B")
	snapshot := func() string {
		var s string
		require.NoError(t, pool.QueryRow(ctx, `SELECT jsonb_build_array((SELECT jsonb_agg(a ORDER BY id) FROM applications a),(SELECT jsonb_agg(g ORDER BY application_id,staff_id) FROM application_staff_grants g),(SELECT jsonb_agg(a ORDER BY id) FROM audit_logs a))::text`).Scan(&s))
		return s
	}
	auditCount := func() int64 {
		var n int64
		require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM audit_logs WHERE actor_user_id=ANY($1)", ids).Scan(&n))
		return n
	}
	var grantCount int64
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM application_staff_grants WHERE application_id=ANY($1)", []int64{a.ID, b.ID}).Scan(&grantCount))
	require.Zero(t, grantCount)
	_, err = service.GetGranted(super, a.PublicID)
	require.ErrorIs(t, err, authz.ErrApplicationNotFound)
	own := map[string]sqlc.Application{}
	for _, role := range []string{"VIEWER", "OPERATOR", "ADMIN", "SUPER_ADMIN"} {
		app := a
		if role == "VIEWER" {
			app = b
		}
		own[role] = app
		g, err := service.EnableGrant(super, app.PublicID, actors[role], applications.Preconditions{IfNoneMatch: "*"})
		require.NoError(t, err)
		require.True(t, g.Created)
	}
	for role, id := range actors {
		t.Run(role, func(t *testing.T) {
			c := identity(id)
			before := snapshot()
			page, err := service.ListGranted(c, 1, 20)
			require.NoError(t, err)
			require.Equal(t, int64(1), page.Total)
			require.Equal(t, own[role].PublicID, page.Items[0].PublicID)
			app, err := service.GetGranted(c, own[role].PublicID)
			require.NoError(t, err)
			require.Equal(t, own[role].ID, app.ID)
			foreign := b
			if own[role].ID == b.ID {
				foreign = a
			}
			_, err = service.GetGranted(c, foreign.PublicID)
			require.ErrorIs(t, err, authz.ErrApplicationNotFound)
			if role != "SUPER_ADMIN" {
				ops := []func() error{
					func() error { _, e := service.ListManaged(c, "", 1, 20); return e }, func() error { _, e := service.GetManaged(c, a.PublicID); return e },
					func() error { _, e := service.Create(c, "forbidden-app", "Denied"); return e }, func() error {
						_, e := service.Rename(c, a.PublicID, "Denied", applications.Preconditions{IfMatch: applications.ApplicationETag(a)})
						return e
					},
					func() error { _, e := service.ListGrants(c, a.PublicID, 1, 20); return e }, func() error { _, e := service.GetGrant(c, a.PublicID, id); return e },
					func() error {
						_, e := service.EnableGrant(c, b.PublicID, id, applications.Preconditions{IfNoneMatch: "*"})
						return e
					}, func() error {
						return service.RevokeGrant(c, a.PublicID, id, applications.Preconditions{IfMatch: `"tag"`})
					},
				}
				for _, op := range ops {
					require.ErrorIs(t, op(), authz.ErrForbidden)
				}
			}
			require.Equal(t, before, snapshot())
		})
	}
	t.Run("metadata preconditions and no-ops", func(t *testing.T) {
		before := auditCount()
		same, err := service.Rename(super, a.PublicID, "  Aplikasi A  ", applications.Preconditions{IfMatch: applications.ApplicationETag(a)})
		require.NoError(t, err)
		require.Equal(t, a.Revision, same.Revision)
		require.Equal(t, before, auditCount())
		renamed, err := service.Rename(super, a.PublicID, "Aplikasi A baru", applications.Preconditions{IfMatch: applications.ApplicationETag(a)})
		require.NoError(t, err)
		require.Equal(t, a.Revision+1, renamed.Revision)
		invalid, err := service.Rename(super, a.PublicID, "Stale", applications.Preconditions{IfMatch: applications.ApplicationETag(a)})
		require.ErrorIs(t, err, applications.ErrPreconditionFailed)
		require.Zero(t, invalid.ID)
		a = renamed
		_, err = service.Rename(super, a.PublicID, "Missing", applications.Preconditions{})
		require.ErrorIs(t, err, applications.ErrPreconditionRequired)
		_, err = service.Rename(super, a.PublicID, "Weak", applications.Preconditions{IfMatch: `W/"tag"`})
		require.ErrorIs(t, err, applications.ErrInvalidPrecondition)
		_, err = service.Create(super, a.Key, "Duplicate")
		require.ErrorIs(t, err, applications.ErrKeyExists)
	})
	t.Run("grant no-op revoke regrant", func(t *testing.T) {
		g, err := service.GetGrant(super, a.PublicID, actors["ADMIN"])
		require.NoError(t, err)
		before := auditCount()
		noOp, err := service.EnableGrant(super, a.PublicID, g.StaffID, applications.Preconditions{IfMatch: applications.GrantETag(a, g)})
		require.NoError(t, err)
		require.False(t, noOp.Created)
		require.Equal(t, g.Revision, noOp.Grant.Revision)
		require.Equal(t, before, auditCount())
		require.NoError(t, service.RevokeGrant(super, a.PublicID, g.StaffID, applications.Preconditions{IfMatch: applications.GrantETag(a, g)}))
		_, err = service.GetGranted(identity(g.StaffID), a.PublicID)
		require.ErrorIs(t, err, authz.ErrApplicationNotFound)
		revoked, err := service.GetGrant(super, a.PublicID, g.StaffID)
		require.NoError(t, err)
		require.False(t, revoked.IsActive)
		require.Equal(t, g.Revision+1, revoked.Revision)
		before = auditCount()
		require.NoError(t, service.RevokeGrant(super, a.PublicID, g.StaffID, applications.Preconditions{IfMatch: applications.GrantETag(a, revoked)}))
		require.Equal(t, before, auditCount())
		_, err = service.EnableGrant(super, a.PublicID, g.StaffID, applications.Preconditions{IfMatch: applications.GrantETag(a, g)})
		require.ErrorIs(t, err, applications.ErrPreconditionFailed)
		active, err := service.EnableGrant(super, a.PublicID, g.StaffID, applications.Preconditions{IfMatch: applications.GrantETag(a, revoked)})
		require.NoError(t, err)
		require.Equal(t, int64(3), active.Grant.Revision)
		require.ErrorIs(t, service.RevokeGrant(super, a.PublicID, g.StaffID, applications.Preconditions{IfMatch: applications.GrantETag(a, g)}), applications.ErrPreconditionFailed)
	})
	// CHECK NOT VALID enforces new rows only; existing fixture audits are preserved.
	// The constraint is scoped to this fixture actor and removed after each fault.
	fault := func(t *testing.T, action string, fn func() error) {
		ddl := fmt.Sprintf("ALTER TABLE audit_logs ADD CONSTRAINT fixture_registry_audit_failure CHECK (NOT(actor_user_id=%d AND action='%s')) NOT VALID", actors["SUPER_ADMIN"], action)
		_, err := pool.Exec(ctx, ddl)
		require.NoError(t, err)
		defer func() {
			_, err := pool.Exec(context.Background(), "ALTER TABLE audit_logs DROP CONSTRAINT fixture_registry_audit_failure")
			require.NoError(t, err)
		}()
		before := snapshot()
		err = fn()
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr)
		require.Equal(t, "23514", pgErr.Code)
		require.Equal(t, before, snapshot(), "audit failure left partial mutation")
	}
	t.Run("create audit rollback", func(t *testing.T) {
		keys = append(keys, "m03-service-rollback")
		fault(t, audit.ACTION_APPLICATION_CREATE, func() error {
			v, e := service.Create(super, "m03-service-rollback", "Rollback")
			require.Zero(t, v.ID)
			return e
		})
	})
	t.Run("rename audit rollback", func(t *testing.T) {
		fault(t, audit.ACTION_APPLICATION_UPDATE, func() error {
			v, e := service.Rename(super, a.PublicID, "Rollback", applications.Preconditions{IfMatch: applications.ApplicationETag(a)})
			require.Zero(t, v.ID)
			return e
		})
	})
	t.Run("new grant audit rollback", func(t *testing.T) {
		fault(t, audit.ACTION_APPLICATION_GRANT_ENABLE, func() error {
			v, e := service.EnableGrant(super, b.PublicID, actors["ADMIN"], applications.Preconditions{IfNoneMatch: "*"})
			require.Zero(t, v.Grant.ApplicationID)
			return e
		})
	})
	t.Run("revoke audit rollback", func(t *testing.T) {
		g, e := service.GetGrant(super, a.PublicID, actors["ADMIN"])
		require.NoError(t, e)
		fault(t, audit.ACTION_APPLICATION_GRANT_REVOKE, func() error {
			return service.RevokeGrant(super, a.PublicID, g.StaffID, applications.Preconditions{IfMatch: applications.GrantETag(a, g)})
		})
	})
	t.Run("reactivation audit rollback", func(t *testing.T) {
		g, e := service.GetGrant(super, a.PublicID, actors["ADMIN"])
		require.NoError(t, e)
		require.NoError(t, service.RevokeGrant(super, a.PublicID, g.StaffID, applications.Preconditions{IfMatch: applications.GrantETag(a, g)}))
		g, e = service.GetGrant(super, a.PublicID, g.StaffID)
		require.NoError(t, e)
		fault(t, audit.ACTION_APPLICATION_GRANT_ENABLE, func() error {
			_, e := service.EnableGrant(super, a.PublicID, g.StaffID, applications.Preconditions{IfMatch: applications.GrantETag(a, g)})
			return e
		})
	})
	t.Run("concurrent rename", func(t *testing.T) {
		current, e := service.GetManaged(super, a.PublicID)
		require.NoError(t, e)
		ch := make(chan error, 2)
		before := auditCount()
		for _, name := range []string{"Concurrent A", "Concurrent B"} {
			go func(n string) {
				_, e := service.Rename(super, a.PublicID, n, applications.Preconditions{IfMatch: applications.ApplicationETag(current)})
				ch <- e
			}(name)
		}
		success, stale := 0, 0
		for i := 0; i < 2; i++ {
			e := <-ch
			if e == nil {
				success++
			} else {
				require.ErrorIs(t, e, applications.ErrPreconditionFailed)
				stale++
			}
		}
		require.Equal(t, 1, success)
		require.Equal(t, 1, stale)
		require.Equal(t, before+1, auditCount())
	})
	t.Run("concurrent first grant", func(t *testing.T) {
		target := seed("VIEWER")
		ch := make(chan error, 2)
		before := auditCount()
		for i := 0; i < 2; i++ {
			go func() {
				_, e := service.EnableGrant(super, a.PublicID, target, applications.Preconditions{IfNoneMatch: "*"})
				ch <- e
			}()
		}
		success, stale := 0, 0
		for i := 0; i < 2; i++ {
			e := <-ch
			if e == nil {
				success++
			} else {
				require.ErrorIs(t, e, applications.ErrPreconditionFailed)
				stale++
			}
		}
		require.Equal(t, 1, success)
		require.Equal(t, 1, stale)
		require.Equal(t, before+1, auditCount())
	})
	t.Run("inactive and deleted targets", func(t *testing.T) {
		target := seed("VIEWER")
		_, e := pool.Exec(ctx, "UPDATE staff_users SET is_active=false WHERE id=$1", target)
		require.NoError(t, e)
		before := snapshot()
		_, e = service.EnableGrant(super, b.PublicID, target, applications.Preconditions{IfNoneMatch: "*"})
		require.ErrorIs(t, e, applications.ErrStaffInactive)
		_, e = pool.Exec(ctx, "DELETE FROM staff_users WHERE id=$1", target)
		require.NoError(t, e)
		_, e = service.EnableGrant(super, b.PublicID, target, applications.Preconditions{IfNoneMatch: "*"})
		require.ErrorIs(t, e, applications.ErrStaffNotFound)
		require.Equal(t, before, snapshot())
	})
	t.Run("target deactivation while enabling a grant", func(t *testing.T) {
		target := seed("VIEWER")
		before := auditCount()
		blocker, err := pool.Begin(ctx)
		require.NoError(t, err)
		defer blocker.Rollback(context.Background())
		_, err = blocker.Exec(ctx, "UPDATE staff_users SET is_active=false WHERE id=$1", target)
		require.NoError(t, err)
		result := make(chan error, 1)
		go func() {
			_, err := service.EnableGrant(super, b.PublicID, target, applications.Preconditions{IfNoneMatch: "*"})
			result <- err
		}()
		require.Eventually(t, func() bool {
			var blocked bool
			err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))",
				int32(blocker.Conn().PgConn().PID())).Scan(&blocked)
			return err == nil && blocked
		}, 2*time.Second, 10*time.Millisecond)
		require.NoError(t, blocker.Commit(ctx))
		select {
		case err := <-result:
			require.ErrorIs(t, err, applications.ErrStaffInactive)
		case <-ctx.Done():
			t.Fatal("grant did not return after target deactivation")
		}
		_, err = service.GetGrant(super, b.PublicID, target)
		require.ErrorIs(t, err, applications.ErrGrantNotFound)
		require.Equal(t, before, auditCount())
	})

	t.Run("audit safe scope and actor", func(t *testing.T) {
		rows, e := pool.Query(ctx, "SELECT application_id,actor_user_id,entity_type,before_json,after_json FROM audit_logs WHERE actor_user_id=ANY($1)", ids)
		require.NoError(t, e)
		defer rows.Close()
		count := 0
		for rows.Next() {
			var appID, actorID int64
			var entity string
			var before, after []byte
			require.NoError(t, rows.Scan(&appID, &actorID, &entity, &before, &after))
			count++
			require.Positive(t, appID)
			require.Equal(t, actors["SUPER_ADMIN"], actorID)
			allowed := map[string]bool{"id": true, "key": true, "name": true, "status": true, "revision": true, "created_at": true, "updated_at": true}
			if entity == audit.ENTITY_TYPE_APPLICATION_GRANT {
				allowed = map[string]bool{"application_id": true, "staff_id": true, "is_active": true, "revision": true, "created_at": true, "updated_at": true}
			}
			for _, data := range [][]byte{before, after} {
				if data == nil {
					continue
				}
				var payload map[string]any
				require.NoError(t, json.Unmarshal(data, &payload))
				for k := range payload {
					require.True(t, allowed[k], "unsafe audit field %s", k)
				}
				require.NotContains(t, string(data), "fixture-only")
			}
		}
		require.NoError(t, rows.Err())
		require.Positive(t, count)
	})
	t.Run("transaction authority recheck", func(t *testing.T) {
		keys = append(keys, "m03-service-recheck")
		wrapped := applications.NewService(q, demotingTransactor{pool: pool, actor: actors["SUPER_ADMIN"]})
		defer func() {
			_, e := pool.Exec(context.Background(), "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='SUPER_ADMIN') WHERE id=$1", actors["SUPER_ADMIN"])
			require.NoError(t, e)
		}()
		before := snapshot()
		result, e := wrapped.Create(super, "m03-service-recheck", "Denied after precheck")
		require.ErrorIs(t, e, authz.ErrUnauthenticated)
		require.Zero(t, result.ID)
		require.Equal(t, before, snapshot())
	})

	t.Run("committed role demotion", func(t *testing.T) {
		_, e := pool.Exec(ctx, "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='VIEWER') WHERE id=$1", actors["SUPER_ADMIN"])
		require.NoError(t, e)
		before := snapshot()
		_, e = service.Create(identity(actors["SUPER_ADMIN"]), "denied-app", "Denied")
		require.ErrorIs(t, e, authz.ErrForbidden)
		require.Equal(t, before, snapshot())
	})
	// No activation path exists even with a granted SUPER_ADMIN.
	scope := authz.NewApplicationAuthorizer(q)
	_, err = scope.RequireApplication(identity(actors["OPERATOR"]), a.PublicID, authz.NotificationsWrite)
	require.ErrorIs(t, err, authz.ErrApplicationNotActive)
	// Ensure a foreign syntactically valid app UUID never defaults to another app.
	_, err = service.GetGranted(identity(actors["OPERATOR"]), pgtype.UUID{Bytes: [16]byte{9}, Valid: true})
	require.ErrorIs(t, err, authz.ErrApplicationNotFound)
}

// Commit demotion after the service precheck and before its transaction starts.
// Real pool/query bindings prove the inner check is independent of the precheck.
type demotingTransactor struct {
	pool  *pgxpool.Pool
	actor int64
}

func (d demotingTransactor) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	if _, err := d.pool.Exec(ctx, "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='VIEWER') WHERE id=$1", d.actor); err != nil {
		return nil, err
	}
	return d.pool.BeginTx(ctx, opts)
}
