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

// ListMembers returns paginated list of members in a segment.
func (s *MembersService) ListMembers(ctx context.Context, segmentID int64, page, limit int32) ([]MemberListItem, int64, error) {
	// Check if segment exists
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

	items := make([]MemberListItem, len(rows))
	for i, row := range rows {
		items[i] = MemberListItem{
			ID:        row.ID,
			UserID:    row.UserID,
			Name:      row.Name.String,
			Email:     row.Email.String,
			CreatedAt: row.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
		}
	}

	return items, total, nil
}

// AddMember adds a user to a segment.
func (s *MembersService) AddMember(ctx context.Context, segmentID, userID int64) error {
	// Check if segment exists
	_, err := s.segmentRepo.FindByID(ctx, segmentID)
	if err != nil {
		return ErrSegmentNotFound
	}

	// Check if user exists
	_, err = s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return ErrUserNotFound
	}

	// Check if already a member (optional, but prevents duplicate)
	// We can query existing members but we don't have a method to check existence by segment+user.
	// We'll try to insert and catch duplicate key error, or we can query.
	// For simplicity, we'll rely on database unique constraint and handle error.
	_, err = s.memberRepo.Create(ctx, sqlc.CreateSegmentMemberParams{
		SegmentID: segmentID,
		UserID:    userID,
	})
	if err != nil {
		// Check if error is duplicate key (PostgreSQL error code 23505)
		// But we can also check existence first to give a clear error.
		// I'll do existence check to provide better error message.
		// Since we don't have a method, we can list with limit=1 and filter.
		// For production, better to have a dedicated Exists method.
		// I'll implement a quick check by listing members and filtering.
		members, _ := s.memberRepo.ListBySegment(ctx, segmentID, 0, 1000)
		for _, m := range members {
			if m.UserID == userID {
				return ErrMemberAlreadyExists
			}
		}
		return fmt.Errorf("add member: %w", err)
	}

	return nil
}

// RemoveMember removes a user from a segment.
func (s *MembersService) RemoveMember(ctx context.Context, segmentID, userID int64) error {
	// Check if segment exists
	_, err := s.segmentRepo.FindByID(ctx, segmentID)
	if err != nil {
		return ErrSegmentNotFound
	}

	// Check if user exists
	_, err = s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return ErrUserNotFound
	}

	// Delete by segment and user
	err = s.memberRepo.DeleteBySegmentAndUser(ctx, segmentID, userID)
	if err != nil {
		// If no rows affected, consider it not found
		return ErrMemberNotFound
	}

	return nil
}
