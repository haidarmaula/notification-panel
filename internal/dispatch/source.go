package dispatch

import (
	"context"
	"errors"
	"strings"
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
