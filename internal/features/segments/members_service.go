package segments

import (
	"context"
	"errors"
	"fmt"
	"hello/internal/authz"

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
	// already a member of the segment. The service checks membership using
	// SegmentMemberRepository.Exists, which inspects the exact
	// (segment_id, user_id) pair rather than an arbitrary page.
	//
	// The handler maps this to 409 Conflict.
	ErrMemberAlreadyExists = errors.New("user is already a member of this segment")

	// ErrMemberNotFound is returned by RemoveMember when the user is not
	// a member of the segment. The service performs an explicit Exists
	// check before deleting, so removing a non-member now returns this
	// sentinel instead of reporting success.
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
// # Authorization
//
// Every method requires live staff authority: members.read for ListMembers
// and members.write for AddMember/RemoveMember, before any domain lookup.
// Token roles and previous route checks are not trusted.
//
// # Concurrency
//
// The service is stateless after construction and safe for concurrent use.
// It holds no per-request state and shares nothing mutable between
// invocations. The three repositories it holds are themselves safe for
// concurrent use.
type MembersService struct {
	authorization authz.Checker
	segmentRepo   *repository.SegmentRepository
	memberRepo    *repository.SegmentMemberRepository
	userRepo      *repository.UserRepository
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
	authorization authz.Checker,
) *MembersService {
	if authorization == nil {
		panic("members: authorization checker required")
	}
	return &MembersService{
		authorization: authorization,
		segmentRepo:   segmentRepo,
		memberRepo:    memberRepo,
		userRepo:      userRepo,
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
	if _, err := s.authorization.Require(ctx, authz.MembersRead); err != nil {
		return nil, 0, err
	}
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
// # Existing-membership check
//
// The service calls SegmentMemberRepository.Exists for the exact
// (segment_id, user_id) pair. This avoids the former page-limited
// ListBySegment fallback and therefore works no matter how many members the
// segment already has.
//
// The Exists check and the following insert are not wrapped in a database
// transaction. Concurrent AddMember calls for the same pair may both pass
// the Exists check; the database unique constraint, if present, remains the
// authoritative guard.
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
// for the same (segment, user) pair will both pass the Exists check and
// both attempt the insert; the outcome is the same as for a duplicate add
// by a single caller.
func (s *MembersService) AddMember(ctx context.Context, segmentID, userID int64) error {
	if _, err := s.authorization.Require(ctx, authz.MembersWrite); err != nil {
		return err
	}
	_, err := s.segmentRepo.FindByID(ctx, segmentID)
	if err != nil {
		return ErrSegmentNotFound
	}

	_, err = s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return ErrUserNotFound
	}

	exists, err := s.memberRepo.Exists(ctx, segmentID, userID)
	if err != nil {
		return fmt.Errorf("check membership: %w", err)
	}
	if exists {
		return ErrMemberAlreadyExists
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
//  3. Check that the (segment, user) membership exists; if not, return
//     ErrMemberNotFound.
//  4. Delete the membership row via memberRepo.DeleteBySegmentAndUser.
//
// # Membership existence is verified before delete
//
// RemoveMember calls SegmentMemberRepository.Exists before issuing the
// delete. A pair that does not exist returns ErrMemberNotFound. A database
// failure from Exists is wrapped rather than collapsed into the not-found
// sentinel. The subsequent delete remains a no-op if another request
// removed the pair between the two calls, which is harmless for this API.
//
// # Errors
//
// Returns ErrSegmentNotFound or ErrUserNotFound for a missing segment or
// user. Returns ErrMemberNotFound when the membership does not exist.
// Repository failures during the existence check or delete are wrapped and
// are not collapsed into ErrMemberNotFound.
//
// # Concurrency
//
// Not serialised against concurrent removals. Two concurrent RemoveMember
// calls for the same (segment, user) pair are both no-op-safe: the first
// matches and deletes the row; the second matches zero rows and returns
// nil.
func (s *MembersService) RemoveMember(ctx context.Context, segmentID, userID int64) error {
	if _, err := s.authorization.Require(ctx, authz.MembersWrite); err != nil {
		return err
	}
	_, err := s.segmentRepo.FindByID(ctx, segmentID)
	if err != nil {
		return ErrSegmentNotFound
	}

	_, err = s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return ErrUserNotFound
	}

	exists, err := s.memberRepo.Exists(ctx, segmentID, userID)
	if err != nil {
		return fmt.Errorf("check membership: %w", err)
	}
	if !exists {
		return ErrMemberNotFound
	}

	if err := s.memberRepo.DeleteBySegmentAndUser(ctx, segmentID, userID); err != nil {
		return fmt.Errorf("remove member: %w", err)
	}

	return nil
}
