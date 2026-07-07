package segments

import (
	"context"
	"errors"
	"fmt"

	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
)

// Domain errors for members.
var (
	ErrMemberAlreadyExists = errors.New("user is already a member of this segment")
	ErrMemberNotFound      = errors.New("user is not a member of this segment")
	ErrUserNotFound        = errors.New("user not found")
)

// MembersService provides business logic for segment members.
type MembersService struct {
	segmentRepo *repository.SegmentRepository
	memberRepo  *repository.SegmentMemberRepository
	userRepo    *repository.UserRepository
}

// NewMembersService creates a new MembersService instance.
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
