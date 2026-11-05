package dispatch

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
)

// SourceManifest describes an operator-reviewed physical log source. Validation
// checks syntax only; DB approval and live broker metadata must also agree. The
// pinned Kafka SDK cannot independently establish a topic incarnation UUID.
type SourceManifest struct {
	ID             pgtype.UUID
	ClusterID      string
	Topic          string
	Incarnation    string
	PartitionCount int32
}

// Validate checks a complete source descriptor without I/O or authority changes.
func (s SourceManifest) Validate() error {
	if !s.ID.Valid || s.ID.Bytes == [16]byte{} || s.PartitionCount < 1 || len(s.Topic) < 1 || len(s.Topic) > 249 || s.Topic == "." || s.Topic == ".." {
		return ErrQuarantineSource
	}
	for _, value := range []string{s.ClusterID, s.Incarnation} {
		if len(value) < 1 || len(value) > 255 || !utf8.ValidString(value) || strings.TrimSpace(value) != value || strings.ContainsFunc(value, unicode.IsControl) {
			return ErrQuarantineSource
		}
	}
	for _, r := range s.Topic {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return ErrQuarantineSource
		}
	}
	return nil
}

// approved locks the immutable registration after the caller's runtime lock and
// before any outbox/application/child lock. The caller owns the transaction.
func (s SourceManifest) approved(ctx context.Context, q *sqlc.Queries) (bool, error) {
	row, err := q.LockSendLogSource(ctx, s.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return row.Active && row.ClusterIdentity == s.ClusterID && row.Topic == s.Topic && row.TopicIncarnation == s.Incarnation, nil
}

// ErrRuntimeSealed identifies an operator-sealed deployment at startup.
var ErrRuntimeSealed = errors.New("notification runtime is sealed")

// VerifyRegistration checks runtime and source in one bounded read transaction.
// This is startup preflight, not permission for later I/O: publication must still
// hold its own runtime/source locks and compare live broker metadata.
func (s SourceManifest) VerifyRegistration(ctx context.Context, db Transactor) error {
	if db == nil || s.Validate() != nil {
		return ErrQuarantineSource
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := sqlc.New(tx)
	runtime, err := q.LockNotificationRuntime(ctx)
	if err != nil {
		return err
	}
	if runtime.Sealed {
		return ErrRuntimeSealed
	}
	approved, err := s.approved(ctx, q)
	if err != nil {
		return err
	}
	if !approved {
		return ErrQuarantineSource
	}
	return tx.Commit(ctx)
}
