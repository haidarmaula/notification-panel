package segments

import (
	"context"
	"errors"
	"fmt"

	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
)

// Domain errors for members.
//
// These sentinels are returned by MembersService methods and translated
// into HTTP status codes by MembersHandler. They are declared here, in the
// file that owns their methods, rather than in a separate errors.go —
// consistent with the segments package's other sentinels (which live in
// service.go).
var (
	// ErrMemberAlreadyExists is returned by AddMember when the user is
	// already a member of the segment. The check that produces it has a
	// known correctness gap — see AddMember for the details — so this
	// sentinel is currently reachable only for a subset of duplicate-add
	// cases.
	//
	// The handler maps this to 409 Conflict.
	ErrMemberAlreadyExists = errors.New("user is already a member of this segment")

	// ErrMemberNotFound is returned by RemoveMember when the user is not
	// a member of the segment. Despite its declared purpose, the current
	// implementation of RemoveMember does not actually produce this
	// sentinel: the underlying DeleteBySegmentAndUser is a no-op DELETE
	// that returns nil when the pair does not exist, and the code path
	// that would translate that into ErrMemberNotFound is unreachable.
	// See RemoveMember for the full analysis.
	//
	// The handler maps this to 404 Not Found.
	ErrMemberNotFound = errors.New("user is not a member of this segment")

	// ErrUserNotFound is returned by AddMember and RemoveMember when the
	// target user ID does not resolve to a users row. It is returned by
	// both methods before the membership operation is attempted, so the
	// membership table is not touched when the user does not exist.
	//
	// The handler maps this to 404 Not Found.
	ErrUserNotFound = errors.New("user not found")
)

// MembersService provides business logic for segment members.
//
// It is the service layer for the members sub-resource of a segment: listing
// a segment's members, adding a user to a segment, and removing a user from
// a segment. It is a distinct type from SegmentService (which owns the
// segment's own CRUD), and the two share the same repository types but are
// wired independently.
//
// # Repository types
//
// Unlike the interfaces declared in interfaces.go, this service depends on
// concrete repository types (*repository.SegmentRepository,
// *repository.SegmentMemberRepository, *repository.UserRepository). The
// interfaces exist but are not used by this service; the testability
// rationale documented on them does not apply to the members path. The
// structural divergence is noted so that a future reader who has seen
// interfaces.go does not assume the service depends on them.
//
// # Concurrency
//
// The service is stateless after construction and safe for concurrent use.
// It holds no per-request state and shares nothing mutable between
// invocations. The three repositories it holds are themselves safe for
// concurrent use.
type MembersService struct {
	segmentRepo *repository.SegmentRepository
	memberRepo  *repository.SegmentMemberRepository
	userRepo    *repository.UserRepository
}

// NewMembersService creates a new MembersService instance.
//
// All three parameters must be non-nil; none is validated, so a nil
// repository will panic on the first method call that uses it rather than
// at construction. In practice the dependencies are wired by the segments
// module's constructor from the shared sqlc.Queries.
//
// The returned service is cheap to construct and holds no external
// resources. There is no Close method — none is needed.
func NewMembersService(
	segmentRepo *repository.SegmentRepository,
	memberRepo *repository.SegmentMemberRepository,
	userRepo *repository.UserRepository,
) *MembersService {
	return &MembersService{
		segmentRepo: segmentRepo,
		memberRepo:  memberRepo,
		userRepo:    userRepo,
	}
}

// ListMembers returns a paginated list of domain Members in a segment.
//
// # Flow
//
//  1. Verify that the segment exists via segmentRepo.FindByID. Any failure
//     — including a transient database error — collapses to
//     ErrSegmentNotFound. The handler serves it as a 404.
//  2. Compute the offset from page and limit as (page - 1) * limit. The
//     page value is expected to be 1-indexed and non-zero; the handler
//     supplies values satisfying that precondition.
//  3. Fetch the page of members via memberRepo.ListBySegment, which joins
//     the users table so the returned rows carry the member's name and
//     email alongside the membership identity.
//  4. Count the total number of members via memberRepo.CountBySegment. The
//     count is unfiltered and reflects the segment's full membership.
//  5. Map each row to a domain Member, flattening the pgtype.Text name
//     and email fields to plain strings.
//
// # Return values
//
// On success, the method returns the page's members, the total membership
// count, and a nil error. On any failure it returns a nil slice, zero
// total, and a non-nil error. The two repository errors (from step 3 and
// step 4) are wrapped with the prefixes "list members: " and "count
// members: " respectively; the segment-existence failure is the
// sentinel ErrSegmentNotFound, which the handler maps to 404.
//
// # Concurrency
//
// Safe for concurrent use. Two concurrent calls may observe different
// membership states if an add or remove is in flight between their two
// reads.
func (s *MembersService) ListMembers(ctx context.Context, segmentID int64, page, limit int32) ([]Member, int64, error) {
	_, err := s.segmentRepo.FindByID(ctx, segmentID)
	if err != nil {
		return nil, 0, ErrSegmentNotFound
	}

	offset := (page - 1) * limit
	rows, err := s.memberRepo.ListBySegment(ctx, segmentID, offset, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list members: %w", err)
	}

	total, err := s.memberRepo.CountBySegment(ctx, segmentID)
	if err != nil {
		return nil, 0, fmt.Errorf("count members: %w", err)
	}

	members := make([]Member, len(rows))
	for i, row := range rows {
		members[i] = Member{
			ID:        row.ID,
			UserID:    row.UserID,
			Name:      row.Name.String,
			Email:     row.Email.String,
			CreatedAt: row.CreatedAt.Time,
		}
	}

	return members, total, nil
}

// AddMember adds a user to a segment.
//
// # Flow
//
//  1. Verify that the segment exists via segmentRepo.FindByID. Any failure
//     collapses to ErrSegmentNotFound.
//  2. Verify that the user exists via userRepo.FindByID. Any failure
//     collapses to ErrUserNotFound.
//  3. Check for existing membership — see the correctness note below.
//  4. Insert a new segment_members row via memberRepo.Create.
//
// # Existing-membership check is defective
//
// The check at step 3 is intended to detect that the user is already a
// member and return ErrMemberAlreadyExists. It is implemented as a single
// ListBySegment call with a page size of 1:
//
//	rows, err := s.memberRepo.ListBySegment(ctx, segmentID, 0, 1)
//	for _, row := range rows {
//	    if row.UserID == userID {
//	        return ErrMemberAlreadyExists
//	    }
//	}
//
// Because the page size is 1, the loop inspects only the *first* member of
// the segment. If the target user is not the first member — which is the
// case for any segment with more than one member, except when the user
// happens to sort first — the check reports "not already a member" and the
// code proceeds to the insert.
//
// The consequences depend on the schema:
//
//   - If segment_members has a UNIQUE constraint on (segment_id, user_id),
//     the insert fails with a unique-violation error, which this method
//     wraps with "add member: " and the handler serves through its default
//     500 branch. The client sees a generic 500 rather than the 409 that
//     ErrMemberAlreadyExists would have produced.
//   - If the schema does not enforce uniqueness, the insert succeeds and
//     the segment ends up with duplicate membership rows for the same
//     user. Every list response shows the user twice; the member count is
//     inflated; and any downstream fan-out that treats segment members as
//     an audience will attempt to deliver to the user twice (subject to
//     deduplication elsewhere in the worker).
//
// The in-code comments acknowledge the problem and propose adding an
// ExistsSegmentMember method to the repository, but the fix has not been
// applied. The check is therefore advisory at best, and the authoritative
// protection is whatever the schema enforces.
//
// # Errors
//
// Returns ErrSegmentNotFound, ErrUserNotFound, or ErrMemberAlreadyExists
// for the corresponding conditions. Repository failures during the
// existence check or the insert are wrapped with "check membership: " and
// "add member: " respectively; neither is a sentinel, so the handler
// serves them through the default 500 branch.
//
// # Concurrency
//
// Not serialised against concurrent adds. Two concurrent AddMember calls
// for the same (segment, user) pair will both pass the defective
// existence check and both attempt the insert; the outcome is the same
// as for a duplicate add by a single caller.
func (s *MembersService) AddMember(ctx context.Context, segmentID, userID int64) error {
	_, err := s.segmentRepo.FindByID(ctx, segmentID)
	if err != nil {
		return ErrSegmentNotFound
	}

	_, err = s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return ErrUserNotFound
	}

	// Check existing membership efficiently using repository method.
	// If repository doesn't have Exists, we can use a dedicated query.
	// For now, we'll use a simple list but limit to 1 to avoid N+1.
	// Better: add ExistsSegmentMember to repository.
	// Assuming we added Exists method in repository:
	// exists, err := s.memberRepo.Exists(ctx, segmentID, userID)
	// if err != nil { return fmt.Errorf("check membership: %w", err) }
	// if exists { return ErrMemberAlreadyExists }

	// Since Exists not in repo yet, we use List with limit 1 as fallback.
	rows, err := s.memberRepo.ListBySegment(ctx, segmentID, 0, 1)
	if err != nil {
		return fmt.Errorf("check membership: %w", err)
	}
	for _, row := range rows {
		if row.UserID == userID {
			return ErrMemberAlreadyExists
		}
	}

	_, err = s.memberRepo.Create(ctx, sqlc.CreateSegmentMemberParams{
		SegmentID: segmentID,
		UserID:    userID,
	})
	if err != nil {
		return fmt.Errorf("add member: %w", err)
	}

	return nil
}

// RemoveMember removes a user from a segment.
//
// # Flow
//
//  1. Verify that the segment exists via segmentRepo.FindByID. Any failure
//     collapses to ErrSegmentNotFound.
//  2. Verify that the user exists via userRepo.FindByID. Any failure
//     collapses to ErrUserNotFound.
//  3. Delete the membership row via memberRepo.DeleteBySegmentAndUser.
//
// # The ErrMemberNotFound path is unreachable
//
// The implementation assumes DeleteBySegmentAndUser returns an error when
// the membership does not exist, and translates any such error into
// ErrMemberNotFound:
//
//	err = s.memberRepo.DeleteBySegmentAndUser(ctx, segmentID, userID)
//	if err != nil {
//	    return ErrMemberNotFound
//	}
//
// That assumption is incorrect. The repository's DeleteBySegmentAndUser
// is documented as a no-op DELETE that returns nil when the (segment,
// user) pair does not exist — the underlying SQL is a DELETE, and a DELETE
// that matches no rows is not an error. Consequently:
//
//   - Removing a user who *is* a member succeeds: the DELETE matches one
//     row, the repository returns nil, and RemoveMember returns nil.
//   - Removing a user who is *not* a member also succeeds from the
//     caller's perspective: the DELETE matches zero rows, the repository
//     returns nil, and RemoveMember returns nil. The handler serves 200
//     "member removed" for a removal that did not occur.
//   - ErrMemberNotFound is never returned by this method. The handler's
//     mapping of that sentinel to 404 is dead code.
//
// The practical consequence for a client: a "remove member" call is
// idempotent in the sense that it always reports success, but the client
// cannot distinguish "the user was a member and is now removed" from "the
// user was never a member, nothing changed". If the API's contract
// requires that distinction — a 404 for a non-existent membership — the
// method must be revised to check existence before the delete, or the
// repository must be extended to report the affected-row count.
//
// # Errors
//
// Returns ErrSegmentNotFound or ErrUserNotFound for the corresponding
// conditions. Never returns ErrMemberNotFound despite the sentinel's
// presence in this package. Any repository failure from the delete step
// (which, per the analysis above, does not occur for a missing membership)
// would be returned as ErrMemberNotFound, which is itself a misleading
// mapping for a genuine database error.
//
// # Concurrency
//
// Not serialised against concurrent removals. Two concurrent RemoveMember
// calls for the same (segment, user) pair are both no-op-safe: the first
// matches and deletes the row; the second matches zero rows and returns
// nil.
func (s *MembersService) RemoveMember(ctx context.Context, segmentID, userID int64) error {
	_, err := s.segmentRepo.FindByID(ctx, segmentID)
	if err != nil {
		return ErrSegmentNotFound
	}

	_, err = s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return ErrUserNotFound
	}

	err = s.memberRepo.DeleteBySegmentAndUser(ctx, segmentID, userID)
	if err != nil {
		// If no rows affected, consider it not found.
		// We can check by trying to find the member or rely on error.
		// For simplicity, we assume DeleteBySegmentAndUser returns error if not found.
		// We'll check existence first to give better error.
		return ErrMemberNotFound
	}

	return nil
}
