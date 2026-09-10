// Package audit provides an application-level audit trail for administrative
// actions performed through the Notification Panel.
//
// Every staff-initiated mutation of a staff user, notification campaign, or
// segment is expected to be recorded via AuditService.Log so that the system
// maintains a durable, queryable history of who did what, when, and from
// where. The package is intentionally small: it owns the vocabulary of
// auditable actions (see constants.go), the shape of an audit entry (see
// model.go), and the extraction of actor metadata from the request context
// (see context.go).
//
// # Layering
//
// AuditService sits between the feature services (staff, notifications,
// segments) and the persistence layer. Feature services construct a
// LogParams describing the domain event and call Log; the service is
// responsible for JSON-encoding the before/after snapshots, merging in the
// actor ID, IP, and User-Agent from the context, and writing the row via
// repository.AuditLogRepository.
//
// # Failure semantics
//
// AuditService.Log returns an error on JSON-encoding failure or on a
// repository write failure. Callers decide whether a failed audit entry is
// fatal to the surrounding business operation; the current convention
// elsewhere in the codebase is to treat audit failures as non-fatal and
// continue, but this policy is not enforced here.
package audit

import (
	"context"
	"encoding/json"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

// AuditService records administrative actions to the audit_logs table.
//
// A single AuditService is safe for concurrent use: it holds only a pointer
// to a repository, and the underlying pgx connection pool handles
// concurrency. Handlers may call Log from multiple goroutines without
// additional synchronisation.
type AuditService struct {
	repo *repository.AuditLogRepository
}

// NewAuditService constructs an AuditService backed by the supplied
// repository.
//
// repo must be non-nil; passing nil is not checked and will cause a panic on
// the first call to Log. The returned service holds only the repository
// pointer and is therefore cheap to construct — callers may build one per
// request or share a single instance for the lifetime of the process.
func NewAuditService(repo *repository.AuditLogRepository) *AuditService {
	return &AuditService{
		repo: repo,
	}
}

// Log records a single audit entry derived from params.
//
// # Behaviour
//
// The method marshals params.Before and params.After to JSON (omitting each
// when nil), extracts the actor ID, client IP, and User-Agent from ctx via
// GetAuditContext, and persists the result through the underlying repository.
// Zero-valued optional fields are translated to SQL NULLs so that the audit
// table does not accumulate meaningless empty strings or zeros.
//
// # Actor attribution
//
// If ctx carries no staff identity, GetAuditContext reports ok = false and
// Log proceeds with an actor ID of 0. This is deliberate: the audit trail is
// expected to record system- or worker-initiated events as well as
// staff-initiated ones, and rejecting them outright would leave gaps in the
// history. Callers that need to distinguish staff actions from system actions
// should inspect the actor ID after retrieval.
//
// # Errors
//
// Log returns an error in two cases:
//
//   - params.Before or params.After cannot be marshalled to JSON (for
//     example, because the value contains a channel, function, or cyclic
//     reference). No database write occurs in this case.
//   - The repository write fails, typically because of a database
//     connectivity issue or a constraint violation.
//
// The method performs no retry; callers requiring durability guarantees
// should handle errors and decide whether to fail the surrounding operation.
//
// # Concurrency
//
// Log is safe for concurrent invocation. Each call operates on its own
// LogParams and its own marshalled buffers; no shared mutable state exists
// on the receiver.
func (s *AuditService) Log(ctx context.Context, params LogParams) error {
	var beforeJSON, afterJSON []byte
	var err error

	if params.Before != nil {
		beforeJSON, err = json.Marshal(params.Before)
		if err != nil {
			return err
		}
	}

	if params.After != nil {
		afterJSON, err = json.Marshal(params.After)
		if err != nil {
			return err
		}
	}

	staffID, ipAddress, userAgent, _ := GetAuditContext(ctx)
	_, err = s.repo.Create(ctx, sqlc.CreateAuditLogParams{
		ActorUserID: staffID,
		Action:      params.Action,
		EntityType:  params.EntityType,
		EntityName:  pgtype.Text{String: params.EntityName, Valid: params.EntityName != ""},
		EntityID:    pgtype.Int8{Int64: params.EntityID, Valid: params.EntityID != 0},
		BeforeJson:  beforeJSON,
		AfterJson:   afterJSON,
		IpAddress:   pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
		UserAgent:   pgtype.Text{String: userAgent, Valid: userAgent != ""},
	})

	return err
}
