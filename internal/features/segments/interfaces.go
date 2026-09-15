package segments

import (
	"context"
	"hello/internal/audit"
	"hello/internal/database/sqlc"
)

// SegmentRepository is the segment-persistence contract required by the
// segment feature.
//
// It is declared as an interface rather than depending on the concrete
// *repository.SegmentRepository type. This is a deliberate structural
// divergence from every other feature module in the codebase — auth,
// mobile, notifications, profile, and staff all depend on concrete
// repository types — and it exists for one reason: testability. A mock
// implementation of this interface can be substituted in unit tests
// without a live PostgreSQL instance, and the segment service can be
// exercised against a fake without the full dependency-injection chain.
//
// # Trade-off
//
// The cost of the interface is that it must be kept in sync with the
// concrete repository's method set. Adding a method to
// repository.SegmentRepository does not automatically make it available
// to this package; a corresponding method must be added here if the
// segment service needs it. The signature of each method must match the
// concrete implementation exactly, or the concrete repository will fail to
// satisfy this interface at the point where the module wires them
// together — a compile-time failure that is at least loud, but that
// happens at the wiring site rather than at the interface declaration.
//
// # Method-set provenance
//
// The methods declared here mirror the concrete repository's methods
// one-for-one. Documentation for each method's semantics — pagination
// conventions, error pass-through, NULL handling — is provided on the
// concrete repository in internal/database/repository/segments.go, not
// restated here. Read that file for the contract; this interface only
// establishes which methods the segment service is allowed to depend on.
type SegmentRepository interface {
	Count(ctx context.Context) (int64, error)
	CountSearch(ctx context.Context, keyword string) (int64, error)
	FindByID(ctx context.Context, id int64) (sqlc.Segment, error)
	FindByName(ctx context.Context, name string) (sqlc.Segment, error)
	List(ctx context.Context, offset, limit int32) ([]sqlc.ListSegmentsRow, error)
	Search(ctx context.Context, keyword string, offset, limit int32) ([]sqlc.SearchSegmentsRow, error)
	Create(ctx context.Context, params sqlc.CreateSegmentParams) (sqlc.Segment, error)
	Update(ctx context.Context, params sqlc.UpdateSegmentParams) error
	Delete(ctx context.Context, id int64) error
	ExistsByName(ctx context.Context, name string) (bool, error)
}

// SegmentMemberRepository is the segment-membership persistence contract
// required by the segment feature.
//
// As with SegmentRepository, this is declared as an interface for
// testability rather than depending on the concrete
// *repository.SegmentMemberRepository type. The concrete repository's
// methods are documented in
// internal/database/repository/segment_members.go; this interface only
// establishes which subset the segment feature depends on.
//
// # Current usage
//
// The segment feature uses only a subset of the methods declared here.
// CountBySegment is used by the detail view to populate the member count,
// while CountBySegmentIDs is used by the list view to fetch counts for a
// whole page in one query. The other methods are declared for completeness
// and because a future membership-management feature is expected to consume
// them. An interface with unused methods is a mild code smell — it couples
// the segment feature to methods it does not need — but the alternative
// (split interfaces per consumer) would proliferate types without an
// immediate benefit.
type SegmentMemberRepository interface {
	CountBySegment(ctx context.Context, segmentID int64) (int64, error)
	CountBySegmentIDs(ctx context.Context, segmentIDs []int64) ([]sqlc.CountSegmentMembersBySegmentIDsRow, error)
	CountSegmentsByUser(ctx context.Context, userID int64) (int64, error)
	FindByID(ctx context.Context, id int64) (sqlc.SegmentMember, error)
	ListBySegment(ctx context.Context, segmentID int64, offset, limit int32) ([]sqlc.ListSegmentMembersRow, error)
	ListSegmentsByUser(ctx context.Context, userID int64, offset, limit int32) ([]sqlc.ListSegmentMembersByUserRow, error)
	Create(ctx context.Context, params sqlc.CreateSegmentMemberParams) (sqlc.SegmentMember, error)
	Delete(ctx context.Context, id int64) error
	DeleteBySegmentAndUser(ctx context.Context, segmentID, userID int64) error
}

// StaffUserRepository is the staff-user persistence contract required by
// the segment feature.
//
// The segment feature needs only the ability to load a staff user by ID —
// to resolve the CreatedBy reference in list and detail responses and to
// verify that the acting staff user is active before performing writes.
// The other methods are declared to satisfy the same interface as the
// concrete repository, in case the segment feature needs them in the
// future.
//
// As with the other interfaces, this is a testability seam rather than an
// authoritative contract; the concrete repository's documentation in
// internal/database/repository/staff_users.go is the reference for method
// semantics.
type StaffUserRepository interface {
	Count(ctx context.Context) (int64, error)
	FindByID(ctx context.Context, id int64) (sqlc.StaffUser, error)
	FindByEmail(ctx context.Context, email string) (sqlc.StaffUser, error)
	List(ctx context.Context, offset, limit int32) ([]sqlc.ListStaffUsersRow, error)
	Search(ctx context.Context, keyword string, offset, limit int32) ([]sqlc.SearchStaffUsersRow, error)
	Create(ctx context.Context, params sqlc.CreateStaffUserParams) (sqlc.StaffUser, error)
	Update(ctx context.Context, params sqlc.UpdateStaffUserParams) error
	UpdatePassword(ctx context.Context, params sqlc.UpdateStaffPasswordParams) error
	UpdateStatus(ctx context.Context, params sqlc.UpdateStaffStatusParams) error
	Delete(ctx context.Context, id int64) error
	ExistsByEmail(ctx context.Context, email string) (bool, error)
}

// AuditService is the audit-logging contract required by the segment
// feature.
//
// It is a single-method interface that mirrors the concrete
// *audit.AuditService type's Log method exactly. The interface exists so
// that the segment service can be tested without a live audit-log
// repository, and so that a future revision that changes the audit
// package's shape does not ripple into the segment service as long as the
// contract is preserved.
//
// # Failure semantics
//
// The concrete implementation returns a non-nil error when JSON encoding
// of the audit payload fails, or when the underlying repository write
// fails. Callers in this codebase follow the convention of logging an
// audit failure but not aborting the surrounding operation — the segment
// service is expected to do the same, though the exact behaviour is
// documented on the service method that invokes Log.
type AuditService interface {
	Log(ctx context.Context, params audit.LogParams) error
}
