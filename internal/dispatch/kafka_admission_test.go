package dispatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
)

type approvalQueryFixture struct {
	sqlc.DBTX
	check func(context.Context)
	err   error
}

func (f approvalQueryFixture) QueryRow(ctx context.Context, _ string, _ ...any) pgx.Row {
	f.check(ctx)
	return approvalErrorRow{err: f.err}
}

type approvalErrorRow struct{ err error }

func (r approvalErrorRow) Scan(...any) error { return r.err }

func TestKafkaSourceApprovalBoundsDatabaseWait(t *testing.T) {
	for _, name := range []string{"default deadline", "earlier caller deadline"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			var callerDeadline time.Time
			if name == "earlier caller deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Second)
				defer cancel()
				callerDeadline, _ = ctx.Deadline()
			}
			stopped := errors.New("fixture database failure")
			calls := 0
			queries := sqlc.New(approvalQueryFixture{err: stopped, check: func(ctx context.Context) {
				calls++
				deadline, ok := ctx.Deadline()
				require.True(t, ok, "source approval must not wait for the whole generation lifetime")
				require.LessOrEqual(t, time.Until(deadline), 5*time.Second)
				if !callerDeadline.IsZero() {
					require.Equal(t, callerDeadline, deadline)
				}
			}})
			reader := generationAdmissionReader{q: queries}
			require.ErrorIs(t, reader.approved(ctx), stopped)
			require.Equal(t, 1, calls)
		})
	}
}
