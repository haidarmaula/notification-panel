package audit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

type auditQueries struct {
	sqlc.Querier
	calls  int
	params sqlc.CreateAuditLogParams
	err    error
}

func (q *auditQueries) CreateAuditLog(_ context.Context, p sqlc.CreateAuditLogParams) (sqlc.AuditLog, error) {
	q.calls++
	q.params = p
	return sqlc.AuditLog{}, q.err
}

type forbiddenMarshaler struct{ called *bool }

func (v forbiddenMarshaler) MarshalJSON() ([]byte, error) {
	*v.called = true
	return []byte(`{"PasswordHash":"private-fixture"}`), nil
}

func TestStaffAuditRejectsUnsafePayloadBeforeInsert(t *testing.T) {
	marshaled := false
	for _, value := range []any{
		sqlc.StaffUser{PasswordHash: "private-fixture"},
		map[string]string{"password_hash": "private-fixture"},
		forbiddenMarshaler{&marshaled}, []string{"private-fixture"}, "private-fixture",
	} {
		for _, side := range []string{"before", "after"} {
			q := &auditQueries{}
			service := NewAuditService(repository.NewAuditLogRepository(q))
			params := LogParams{EntityType: ENTITY_TYPE_STAFF, Action: ACTION_STAFF_CREATE}
			if side == "before" {
				params.Before = value
			} else {
				params.After = value
			}
			err := service.Log(context.Background(), params)
			require.ErrorIs(t, err, ErrUnsafeStaffSnapshot)
			require.Equal(t, "staff audit requires a safe snapshot", err.Error())
			require.Zero(t, q.calls)
		}
	}
	require.False(t, marshaled, "unsafe custom marshaler must never run")
	for _, entity := range []string{ENTITY_TYPE_STAFF, ENTITY_TYPE_NOTIFICATION} {
		for _, side := range []string{"before", "after"} {
			q := &auditQueries{}
			params := LogParams{EntityType: entity, Action: ACTION_STAFF_UPDATE_PASSWORD}
			if side == "before" {
				params.Before = StaffSnapshot{ID: 1}
			} else {
				params.After = StaffSnapshot{ID: 1}
			}
			require.ErrorIs(t, NewAuditService(repository.NewAuditLogRepository(q)).Log(context.Background(), params), ErrUnsafeStaffSnapshot)
			require.Zero(t, q.calls)
		}
	}
}

func TestStaffAuditSafeProjectionAndMetadata(t *testing.T) {
	when := time.Date(2026, 11, 3, 1, 2, 3, 0, time.UTC)
	snapshot := StaffSnapshot{ID: 2, RoleID: 4, RoleName: "ADMIN", Name: "Fixture", Email: "fixture@example.invalid", IsActive: true, CreatedAt: when, UpdatedAt: when}
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, &token.AccessClaims{StaffID: 1})
	for _, value := range []any{snapshot, &snapshot} {
		q := &auditQueries{}
		s := NewAuditService(repository.NewAuditLogRepository(q))
		require.NoError(t, s.Log(ctx, LogParams{EntityType: ENTITY_TYPE_STAFF, Action: ACTION_STAFF_UPDATE, EntityID: 2, EntityName: "Fixture", Before: value, After: value}))
		require.Equal(t, 1, q.calls)
		require.Equal(t, int64(1), q.params.ActorUserID)
		require.Equal(t, int64(2), q.params.EntityID.Int64)
		require.Equal(t, "Fixture", q.params.EntityName.String)
		require.JSONEq(t, `{"ID":2,"RoleID":4,"RoleName":"ADMIN","Name":"Fixture","Email":"fixture@example.invalid","IsActive":true,"CreatedAt":"2026-11-03T01:02:03Z","UpdatedAt":"2026-11-03T01:02:03Z"}`, string(q.params.BeforeJson))
		require.JSONEq(t, string(q.params.BeforeJson), string(q.params.AfterJson))
	}
	nullQuery := &auditQueries{}
	var absent *StaffSnapshot
	require.NoError(t, NewAuditService(repository.NewAuditLogRepository(nullQuery)).Log(ctx, LogParams{EntityType: ENTITY_TYPE_STAFF, Action: ACTION_STAFF_CREATE, Before: absent}))
	require.Nil(t, nullQuery.params.BeforeJson)
	q := &auditQueries{}
	require.NoError(t, NewAuditService(repository.NewAuditLogRepository(q)).Log(ctx, LogParams{EntityType: ENTITY_TYPE_STAFF, Action: ACTION_STAFF_UPDATE_PASSWORD}))
	require.Nil(t, q.params.BeforeJson)
	require.Nil(t, q.params.AfterJson)
}

func TestOtherAuditEncodingAndFailuresUnchanged(t *testing.T) {
	q := &auditQueries{}
	s := NewAuditService(repository.NewAuditLogRepository(q))
	ctx := context.Background()
	require.NoError(t, s.Log(ctx, LogParams{EntityType: ENTITY_TYPE_SEGMENT, Before: map[string]any{"ID": 9, "nested": []string{"fixture"}}}))
	require.JSONEq(t, `{"ID":9,"nested":["fixture"]}`, string(q.params.BeforeJson))
	require.Nil(t, q.params.AfterJson)
	q.calls = 0
	err := s.Log(ctx, LogParams{EntityType: ENTITY_TYPE_SEGMENT, After: make(chan int)})
	var unsupported *json.UnsupportedTypeError
	require.ErrorAs(t, err, &unsupported)
	require.Zero(t, q.calls)
	q.err = errors.New("fixture repository unavailable")
	require.ErrorIs(t, s.Log(ctx, LogParams{EntityType: ENTITY_TYPE_STAFF, After: StaffSnapshot{}}), q.err)
}
