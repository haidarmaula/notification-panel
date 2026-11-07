package applications

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
)

type dueScheduleQueries struct {
	sqlc.Querier
	list func(context.Context, sqlc.ListDueScopedSchedulesParams) ([]sqlc.ListDueScopedSchedulesRow, error)
}

func (q dueScheduleQueries) ListDueScopedSchedules(ctx context.Context, page sqlc.ListDueScopedSchedulesParams) ([]sqlc.ListDueScopedSchedulesRow, error) {
	return q.list(ctx, page)
}

func TestPromoteDueSchedulesBoundsDiscovery(t *testing.T) {
	stopped := errors.New("fixture discovery failure")
	calls := 0
	service := NewService(dueScheduleQueries{list: func(ctx context.Context, page sqlc.ListDueScopedSchedulesParams) ([]sqlc.ListDueScopedSchedulesRow, error) {
		calls++
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 5*time.Second)
		require.EqualValues(t, 10, page.PageSize)
		return nil, stopped
	}}, panicDB{})
	report, err := service.PromoteDueSchedules(context.Background(), 10)
	require.ErrorIs(t, err, stopped)
	require.Equal(t, PromotionReport{}, report)
	require.Equal(t, 1, calls)
}
func TestPromoteDueSchedulesRejectsInvalidOrCanceledInput(t *testing.T) {
	service := NewService(dueScheduleQueries{list: func(context.Context, sqlc.ListDueScopedSchedulesParams) ([]sqlc.ListDueScopedSchedulesRow, error) {
		t.Fatal("invalid/canceled discovery reached queries")
		return nil, nil
	}}, panicDB{})
	for _, page := range []int32{0, -1, 101} {
		_, err := service.PromoteDueSchedules(context.Background(), page)
		require.Error(t, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.PromoteDueSchedules(ctx, 1)
	require.ErrorIs(t, err, context.Canceled)
}
func TestPromoteDueSchedulesPreservesEarlierDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	want, _ := ctx.Deadline()
	service := NewService(dueScheduleQueries{list: func(ctx context.Context, _ sqlc.ListDueScopedSchedulesParams) ([]sqlc.ListDueScopedSchedulesRow, error) {
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.Equal(t, want, deadline)
		return nil, nil
	}}, panicDB{})
	report, err := service.PromoteDueSchedules(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, PromotionReport{}, report)
}
