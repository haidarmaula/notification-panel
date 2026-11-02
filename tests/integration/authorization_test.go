//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"hello/internal/audit"
	"hello/internal/authz"
	"hello/internal/config"
	"hello/internal/database"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/features/notifications"
	"hello/internal/features/segments"
	"hello/internal/kafka"
	"hello/internal/middleware"
	"hello/internal/token"
)

// Mutations stay in a rollback-only fixture transaction; no application worker runs.
func TestStaffAuthorizationAPI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(context.Background())
	q := sqlc.New(tx)
	_, err = tx.Exec(ctx, "UPDATE roles SET id = id + 100")
	require.NoError(t, err)
	roles := []string{"VIEWER", "OPERATOR", "ADMIN", "SUPER_ADMIN"}
	staffIDs := map[string]int64{}
	for i, role := range roles {
		id := int64(1001 + i)
		staffIDs[role] = id
		_, err = tx.Exec(ctx, `INSERT INTO staff_users(id,role_id,name,email,password_hash) SELECT $1,id,$2::text,$3,'fixture-only' FROM roles WHERE name=$2::text`, id, role, role+"@fixture.invalid")
		require.NoError(t, err)
	}
	adminID := staffIDs["ADMIN"]
	var userID int64
	require.NoError(t, tx.QueryRow(ctx, `INSERT INTO users(external_id,name) VALUES('authz-user','Fixture') RETURNING id`).Scan(&userID))
	staff := repository.NewStaffUserRepository(q)
	checker := authz.New(staff)
	tokens := token.NewTokenManager("fixture-access-key-not-production", "fixture-refresh-key-not-production")
	api := middleware.NewAPIKeyMiddleware("fixture-api-key")
	jwt := middleware.NewJWTMiddleware(tokens, staff)
	auditMW := middleware.NewAuditMiddleware()
	cfg := &config.ServerConfig{KafkaBroker: "127.0.0.1:19092", SendTopic: "notification.send.requested"}
	mux := http.NewServeMux()
	notifications.NewNotificationModule(q, cfg, api.Use, jwt.Use, auditMW.Use).RegisterRoutes(mux)
	segments.NewSegmentModule(q, api.Use, jwt.Use, auditMW.Use).RegisterRoutes(mux)

	request := func(handler http.Handler, method, path, body string, id int64, claimRole string) *httptest.ResponseRecorder {
		access, err := tokens.GenerateAccessToken(id, claimRole, "fixture@fixture.invalid")
		require.NoError(t, err)
		req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
		req.Header.Set("X-API-Key", "fixture-api-key")
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	snapshot := func() string {
		var value string
		require.NoError(t, tx.QueryRow(ctx, `SELECT jsonb_build_array(
   (SELECT jsonb_agg(n ORDER BY id) FROM notifications n),
   (SELECT jsonb_agg(n ORDER BY id) FROM notification_targets n),
   (SELECT jsonb_agg(n ORDER BY id) FROM segments n),
   (SELECT jsonb_agg(n ORDER BY id) FROM segment_members n),
   (SELECT jsonb_agg(n ORDER BY id) FROM audit_logs n),
   (SELECT jsonb_agg(n ORDER BY id) FROM notification_deliveries n),
   (SELECT jsonb_agg(n ORDER BY id) FROM notification_reads n))::text`).Scan(&value))
		return value
	}
	offsets := func() []int64 {
		result := []int64{}
		for _, topic := range []string{"notification.send.requested", "notification.delivery.updated"} {
			result = append(result, lastTopicOffset(t, ctx, topic))
		}
		return result
	}
	seed := func() (int64, int64) {
		var n, s int64
		require.NoError(t, tx.QueryRow(ctx, `INSERT INTO notifications(title,body,status,created_by) VALUES('Fixture','Fixture','DRAFT',$1) RETURNING id`, adminID).Scan(&n))
		require.NoError(t, tx.QueryRow(ctx, `INSERT INTO segments(name,created_by) VALUES($1,$2) RETURNING id`, fmt.Sprintf("seed-%d", n), adminID).Scan(&s))
		return n, s
	}
	denied := func(handler http.Handler, method, path, body string, id int64, want int) {
		before, brokerBefore := snapshot(), offsets()
		w := request(handler, method, path, body, id, "SUPER_ADMIN")
		require.Equal(t, want, w.Code, "%s %s: %s", method, path, w.Body.String())
		require.Equal(t, before, snapshot(), "denied request mutated database")
		require.Equal(t, brokerBefore, offsets(), "denied request published an event")
		require.NotContains(t, w.Body.String(), "SQL")
	}
	data, err := os.ReadFile("../../docs/contracts/staff-permissions.json")
	require.NoError(t, err)
	var policy struct {
		Routes      []struct{ Method, Path, Permission string }
		Permissions map[string][]string
	}
	require.NoError(t, json.Unmarshal(data, &policy))
	require.Len(t, policy.Routes, 14)
	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			for i, route := range policy.Routes {
				t.Run(route.Method+" "+route.Path, func(t *testing.T) {
					n, s := seed()
					if strings.Contains(route.Path, "/members") && route.Method != "POST" {
						_, err := tx.Exec(ctx, "INSERT INTO segment_members(segment_id,user_id) VALUES($1,$2)", s, userID)
						require.NoError(t, err)
					}
					id := n
					if strings.Contains(route.Path, "/segments") {
						id = s
					}
					path := strings.ReplaceAll(strings.ReplaceAll(route.Path, "{id}", strconv.FormatInt(id, 10)), "{userId}", strconv.FormatInt(userID, 10))
					body := fmt.Sprintf(`{"title":"Changed","body":"Fixture","name":"new-%s-%d","user_id":%d}`, role, i, userID)
					allowed := false
					for _, candidate := range policy.Permissions[route.Permission] {
						if candidate == role {
							allowed = true
						}
					}
					if !allowed {
						denied(mux, route.Method, path, body, staffIDs[role], 403)
						return
					}
					w := request(mux, route.Method, path, body, staffIDs[role], "VIEWER")
					want := 200
					if route.Method == "POST" {
						want = 201
					}
					if strings.HasSuffix(route.Path, "/send") {
						want = 202
					}
					require.Equal(t, want, w.Code, "%s", w.Body.String())
				})
			}
		})
	}
	t.Run("schedule fields and null compatibility", func(t *testing.T) {
		for _, role := range roles {
			n, _ := seed()
			path := fmt.Sprintf("/api/v1/notifications/%d", n)
			future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
			for _, target := range []struct{ method, path string }{{"POST", "/api/v1/notifications"}, {"PATCH", path}} {
				body := fmt.Sprintf(`{"title":"Scheduled","body":"Fixture","scheduled_at":%q}`, future)
				if role == "VIEWER" || role == "OPERATOR" {
					denied(mux, target.method, target.path, body, staffIDs[role], 403)
				} else {
					w := request(mux, target.method, target.path, body, staffIDs[role], "VIEWER")
					want := 200
					if target.method == "POST" {
						want = 201
					}
					require.Equal(t, want, w.Code, w.Body.String())
				}
			}
		}
		n, _ := seed()
		_, err := tx.Exec(ctx, "UPDATE notifications SET scheduled_at=$1 WHERE id=$2", time.Now().Add(time.Hour), n)
		require.NoError(t, err)
		var before time.Time
		require.NoError(t, tx.QueryRow(ctx, "SELECT scheduled_at FROM notifications WHERE id=$1", n).Scan(&before))
		w := request(mux, "PATCH", fmt.Sprintf("/api/v1/notifications/%d", n), `{"title":"Only title","scheduled_at":null}`, staffIDs["OPERATOR"], "VIEWER")
		require.Equal(t, 200, w.Code, w.Body.String())
		var after time.Time
		require.NoError(t, tx.QueryRow(ctx, "SELECT scheduled_at FROM notifications WHERE id=$1", n).Scan(&after))
		require.Equal(t, before, after)
		w = request(mux, "POST", "/api/v1/notifications", `{"title":"Draft","body":"Fixture","scheduled_at":null}`, staffIDs["OPERATOR"], "VIEWER")
		require.Equal(t, 201, w.Code, w.Body.String())
		var envelope struct {
			Data notifications.CreateNotificationResponse
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
		require.Equal(t, "DRAFT", envelope.Data.Status)
	})
	t.Run("missing API key or JWT", func(t *testing.T) {
		for _, header := range []string{"X-API-Key", "Authorization"} {
			access, err := tokens.GenerateAccessToken(adminID, "ADMIN", "")
			require.NoError(t, err)
			req := httptest.NewRequest("GET", "/api/v1/segments", nil).WithContext(ctx)
			req.Header.Set("X-API-Key", "fixture-api-key")
			req.Header.Set("Authorization", "Bearer "+access)
			req.Header.Del(header)
			before := snapshot()
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			require.Equal(t, 401, w.Code)
			require.Equal(t, before, snapshot())
		}
	})
	t.Run("inactive deleted and unknown role", func(t *testing.T) {
		_, err := tx.Exec(ctx, "UPDATE staff_users SET is_active=false WHERE id=$1", staffIDs["VIEWER"])
		require.NoError(t, err)
		denied(mux, "POST", "/api/v1/segments", `{"name":"denied"}`, staffIDs["VIEWER"], 401)
		denied(mux, "POST", "/api/v1/segments", `{"name":"denied"}`, 999999, 401)
		_, err = tx.Exec(ctx, "INSERT INTO roles(id,name) VALUES(800,'CUSTOM')")
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `INSERT INTO staff_users(id,role_id,name,email,password_hash) VALUES(1500,800,'Custom','custom@fixture.invalid','fixture')`)
		require.NoError(t, err)
		denied(mux, "GET", "/api/v1/segments", "", 1500, 403)
	})
	// Direct services exercise the same real authority query, without route protection.
	notifRepo := repository.NewNotificationRepository(q)
	targetRepo := repository.NewNotificationTargetRepository(q)
	deliveryRepo := repository.NewNotificationDeliveryRepository(q)
	readRepo := repository.NewNotificationReadRepository(q)
	templateRepo := repository.NewTemplateRepository(q)
	segmentRepo := repository.NewSegmentRepository(q)
	memberRepo := repository.NewSegmentMemberRepository(q)
	users := repository.NewUserRepository(q)
	auditService := audit.NewAuditService(repository.NewAuditLogRepository(q))
	producer := kafka.NewProducer("127.0.0.1:19092", "notification.send.requested")
	defer producer.Close()
	nService := notifications.NewNotificationService(notifRepo, targetRepo, deliveryRepo, readRepo, staff, templateRepo, segmentRepo, auditService, producer, checker)
	sService := segments.NewSegmentService(segmentRepo, memberRepo, staff, auditService, checker)
	mService := segments.NewMembersService(segmentRepo, memberRepo, users, checker)
	actorContext := func(id int64) context.Context {
		return context.WithValue(ctx, middleware.UserContextKey, &token.AccessClaims{StaffID: id, RoleName: "VIEWER"})
	}
	t.Run("direct service denials preserve database and Kafka", func(t *testing.T) {
		_, err := tx.Exec(ctx, "UPDATE staff_users SET is_active=true WHERE id=$1", staffIDs["VIEWER"])
		require.NoError(t, err)
		c := actorContext(staffIDs["VIEWER"])
		n, s := seed()
		_, err = tx.Exec(ctx, "INSERT INTO segment_members(segment_id,user_id) VALUES($1,$2)", s, userID)
		require.NoError(t, err)
		ops := []func() error{
			func() error {
				_, err := nService.Create(c, notifications.CreateParams{Title: "Denied", Body: "Fixture", TargetType: "BROADCAST"})
				return err
			},
			func() error { return nService.Update(c, n, notifications.UpdateParams{}) },
			func() error { return nService.Delete(c, n) }, func() error { return nService.Send(c, n) },
			func() error { _, err := sService.Create(c, segments.CreateParams{Name: "Denied"}); return err },
			func() error { _, err := sService.Update(c, segments.UpdateParams{ID: s}); return err },
			func() error { return sService.Delete(c, s) }, func() error { return mService.AddMember(c, s, userID) }, func() error { return mService.RemoveMember(c, s, userID) },
		}
		for i, op := range ops {
			before, kafkaBefore := snapshot(), offsets()
			require.ErrorIs(t, op(), authz.ErrForbidden, "operation %d", i)
			require.Equal(t, before, snapshot())
			require.Equal(t, kafkaBefore, offsets())
		}
	})

	t.Run("direct creator provenance", func(t *testing.T) {
		c := actorContext(adminID)
		n, err := nService.Create(c, notifications.CreateParams{Title: "Direct", Body: "Fixture", TargetType: "BROADCAST", CreatedBy: staffIDs["VIEWER"]})
		require.NoError(t, err)
		s, err := sService.Create(c, segments.CreateParams{Name: "Direct", CreatedBy: staffIDs["VIEWER"]})
		require.NoError(t, err)
		for _, item := range []struct {
			table, entity, action string
			id                    int64
		}{{"notifications", audit.ENTITY_TYPE_NOTIFICATION, audit.ACTION_NOTIFICATION_CREATE, n.ID}, {"segments", audit.ENTITY_TYPE_SEGMENT, audit.ACTION_SEGMENT_CREATE, s.ID}} {
			var creator, actor int64
			require.NoError(t, tx.QueryRow(ctx, "SELECT created_by FROM "+item.table+" WHERE id=$1", item.id).Scan(&creator))
			require.Equal(t, adminID, creator)
			require.NoError(t, tx.QueryRow(ctx, "SELECT actor_user_id FROM audit_logs WHERE entity_type=$1 AND entity_id=$2 AND action=$3", item.entity, item.id, item.action).Scan(&actor))
			require.Equal(t, adminID, actor)
		}
	})
	t.Run("demotion between route and service", func(t *testing.T) {
		hook := checkerFunc(func(c context.Context, p ...authz.Permission) (authz.Principal, error) {
			principal, err := checker.Require(c, p...)
			if err == nil {
				_, err = tx.Exec(c, "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='VIEWER') WHERE id=$1", adminID)
			}
			return principal, err
		})
		h := middleware.Chain(api.Use, jwt.Use, auditMW.Use)(authz.Guard(hook, authz.NotificationsWrite, notifications.NewNotificationHandler(nService).Create))
		denied(h, "POST", "/api/v1/notifications", `{"title":"Not created","body":"Fixture"}`, adminID, 403)
	})
}

type checkerFunc func(context.Context, ...authz.Permission) (authz.Principal, error)

func (f checkerFunc) Require(ctx context.Context, p ...authz.Permission) (authz.Principal, error) {
	return f(ctx, p...)
}

func TestCommittedAuthorityChanges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	var id int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO staff_users(role_id,name,email,password_hash)
  SELECT id,'Committed fixture','committed-authz@fixture.invalid','fixture' FROM roles WHERE name='ADMIN' RETURNING staff_users.id`).Scan(&id))
	defer func() {
		_, err := pool.Exec(context.Background(), "DELETE FROM staff_users WHERE id=$1", id)
		require.NoError(t, err)
	}()
	authorityConn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer authorityConn.Release()
	checker := authz.New(repository.NewStaffUserRepository(sqlc.New(authorityConn)))
	c := context.WithValue(ctx, middleware.UserContextKey, &token.AccessClaims{StaffID: id, RoleName: "SUPER_ADMIN"})
	_, err = checker.Require(c, authz.NotificationsSend)
	require.NoError(t, err)
	// Separate pool operation commits before the next statement's authority check.
	_, err = pool.Exec(ctx, "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='VIEWER') WHERE id=$1", id)
	require.NoError(t, err)
	_, err = checker.Require(c, authz.NotificationsSend)
	require.ErrorIs(t, err, authz.ErrForbidden)
	_, err = pool.Exec(ctx, "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='ADMIN') WHERE id=$1", id)
	require.NoError(t, err)
	service := notifications.NewNotificationService(nil, nil, nil, nil, nil, nil, nil, nil, nil, checker)
	hook := checkerFunc(func(ctx context.Context, p ...authz.Permission) (authz.Principal, error) {
		principal, err := checker.Require(ctx, p...)
		if err == nil {
			_, err = pool.Exec(ctx, "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='VIEWER') WHERE id=$1", id)
		}
		return principal, err
	})
	handler := authz.Guard(hook, authz.NotificationsWrite, notifications.NewNotificationHandler(service).Create)
	r := httptest.NewRequest("POST", "/api/v1/notifications", strings.NewReader(`{"title":"Denied","body":"Fixture"}`)).WithContext(c)
	w := httptest.NewRecorder()
	handler(w, r)
	require.Equal(t, 403, w.Code)
	_, err = pool.Exec(ctx, "UPDATE staff_users SET is_active=false WHERE id=$1", id)
	require.NoError(t, err)
	_, err = checker.Require(c, authz.NotificationsRead)
	require.ErrorIs(t, err, authz.ErrUnauthenticated)
}
