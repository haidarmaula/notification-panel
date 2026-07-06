package segments

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
)

// Domain errors.
var (
	ErrSegmentNotFound   = errors.New("segment not found")
	ErrSegmentNameTaken  = errors.New("segment name already exists")
	ErrSegmentHasMembers = errors.New("cannot delete segment that has members")
)

// SegmentService provides business logic for segment management.
type SegmentService struct {
	segmentRepo *repository.SegmentRepository
	memberRepo  *repository.SegmentMemberRepository
	staffRepo   *repository.StaffUserRepository
}

// NewSegmentService creates a new SegmentService instance.
func NewSegmentService(
	segmentRepo *repository.SegmentRepository,
	memberRepo *repository.SegmentMemberRepository,
	staffRepo *repository.StaffUserRepository,
) *SegmentService {
	return &SegmentService{
		segmentRepo: segmentRepo,
		memberRepo:  memberRepo,
		staffRepo:   staffRepo,
	}
}

// List returns a paginated list of segments with optional search.
func (s *SegmentService) List(ctx context.Context, page, limit int32, search string) ([]SegmentListItem, int64, error) {
	offset := (page - 1) * limit

	var rows []SegmentListItem
	var total int64
	var err error

	if search != "" {
		searchRows, err := s.segmentRepo.Search(ctx, search, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("search segments: %w", err)
		}
		rows = make([]SegmentListItem, len(searchRows))
		for i, row := range searchRows {
			memberCount, _ := s.memberRepo.CountBySegment(ctx, row.ID)
			rows[i] = SegmentListItem{
				ID:          row.ID,
				Name:        row.Name,
				Description: &row.Description.String,
				CreatedBy:   row.CreatedByName,
				MemberCount: memberCount,
				CreatedAt:   row.CreatedAt.Time,
				UpdatedAt:   row.UpdatedAt.Time,
			}
		}
	} else {
		listRows, err := s.segmentRepo.List(ctx, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("list segments: %w", err)
		}
		rows = make([]SegmentListItem, len(listRows))
		for i, row := range listRows {
			memberCount, _ := s.memberRepo.CountBySegment(ctx, row.ID)
			rows[i] = SegmentListItem{
				ID:          row.ID,
				Name:        row.Name,
				Description: &row.Description.String,
				CreatedBy:   row.CreatedByName,
				MemberCount: memberCount,
				CreatedAt:   row.CreatedAt.Time,
				UpdatedAt:   row.UpdatedAt.Time,
			}
		}
	}

	total, err = s.segmentRepo.Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count segments: %w", err)
	}

	return rows, total, nil
}

// GetByID returns full segment detail with member count and creator info.
func (s *SegmentService) GetByID(ctx context.Context, id int64) (*SegmentDetail, error) {
	segment, err := s.segmentRepo.FindByID(ctx, id)
	if err != nil {
		return nil, ErrSegmentNotFound
	}

	staff, err := s.staffRepo.FindByID(ctx, segment.CreatedBy)
	if err != nil {
		return nil, fmt.Errorf("get creator: %w", err)
	}

	memberCount, err := s.memberRepo.CountBySegment(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("count members: %w", err)
	}

	return &SegmentDetail{
		ID:          segment.ID,
		Name:        segment.Name,
		Description: &segment.Description.String,
		CreatedBy: StaffBrief{
			ID:   staff.ID,
			Name: staff.Name,
		},
		MemberCount: memberCount,
		CreatedAt:   segment.CreatedAt.Time,
		UpdatedAt:   segment.UpdatedAt.Time,
	}, nil
}

// CreateParams holds input for creating a segment.
type CreateParams struct {
	Name        string
	Description *string
	CreatedBy   int64
}

// Create creates a new segment.
func (s *SegmentService) Create(ctx context.Context, params CreateParams) (*CreateSegmentResponse, error) {
	exists, err := s.segmentRepo.ExistsByName(ctx, params.Name)
	if err != nil {
		return nil, fmt.Errorf("check name existence: %w", err)
	}
	if exists {
		return nil, ErrSegmentNameTaken
	}

	var desc pgtype.Text
	if params.Description != nil {
		desc = pgtype.Text{String: *params.Description, Valid: true}
	}

	segment, err := s.segmentRepo.Create(ctx, sqlc.CreateSegmentParams{
		Name:        params.Name,
		Description: desc,
		CreatedBy:   params.CreatedBy,
	})
	if err != nil {
		return nil, fmt.Errorf("create segment: %w", err)
	}

	return &CreateSegmentResponse{ID: segment.ID}, nil
}

// UpdateParams holds input for updating a segment.
type UpdateParams struct {
	ID          int64
	Name        *string
	Description *string
}

// Update updates an existing segment.
func (s *SegmentService) Update(ctx context.Context, params UpdateParams) error {
	existing, err := s.segmentRepo.FindByID(ctx, params.ID)
	if err != nil {
		return ErrSegmentNotFound
	}

	update := sqlc.UpdateSegmentParams{
		ID:          params.ID,
		Name:        existing.Name,
		Description: existing.Description,
	}

	if params.Name != nil {
		if *params.Name != existing.Name {
			exists, err := s.segmentRepo.ExistsByName(ctx, *params.Name)
			if err != nil {
				return fmt.Errorf("check name: %w", err)
			}
			if exists {
				return ErrSegmentNameTaken
			}
			update.Name = *params.Name
		}
	}
	if params.Description != nil {
		update.Description = pgtype.Text{String: *params.Description, Valid: true}
	}

	return s.segmentRepo.Update(ctx, update)
}

// Delete deletes a segment only if it has no members.
func (s *SegmentService) Delete(ctx context.Context, id int64) error {
	_, err := s.segmentRepo.FindByID(ctx, id)
	if err != nil {
		return ErrSegmentNotFound
	}

	count, err := s.memberRepo.CountBySegment(ctx, id)
	if err != nil {
		return fmt.Errorf("check members: %w", err)
	}
	if count > 0 {
		return ErrSegmentHasMembers
	}

	return s.segmentRepo.Delete(ctx, id)
}
