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
func (s *SegmentService) List(ctx context.Context, page, limit int32, search string) ([]SegmentWithCount, int64, error) {
	offset := (page - 1) * limit

	var sqlSegments []sqlc.Segment
	var err error

	if search != "" {
		rows, err := s.segmentRepo.Search(ctx, search, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("search segments: %w", err)
		}
		sqlSegments = make([]sqlc.Segment, len(rows))
		for i, row := range rows {
			// SearchSegmentsRow does not have CreatedBy, set default
			sqlSegments[i] = sqlc.Segment{
				ID:          row.ID,
				Name:        row.Name,
				Description: row.Description,
				CreatedBy:   0, // TODO: add created_by to query
				CreatedAt:   row.CreatedAt,
				UpdatedAt:   row.UpdatedAt,
			}
		}
	} else {
		rows, err := s.segmentRepo.List(ctx, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("list segments: %w", err)
		}
		sqlSegments = make([]sqlc.Segment, len(rows))
		for i, row := range rows {
			// ListSegmentsRow does not have CreatedBy, set default
			sqlSegments[i] = sqlc.Segment{
				ID:          row.ID,
				Name:        row.Name,
				Description: row.Description,
				CreatedBy:   0, // TODO: add created_by to query
				CreatedAt:   row.CreatedAt,
				UpdatedAt:   row.UpdatedAt,
			}
		}
	}

	total, err := s.segmentRepo.Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count segments: %w", err)
	}

	result := make([]SegmentWithCount, len(sqlSegments))
	for i, seg := range sqlSegments {
		memberCount, _ := s.memberRepo.CountBySegment(ctx, seg.ID)
		result[i] = SegmentWithCount{
			Segment: Segment{
				ID:          seg.ID,
				Name:        seg.Name,
				Description: seg.Description.String,
				CreatedBy:   seg.CreatedBy,
				CreatedAt:   seg.CreatedAt.Time,
				UpdatedAt:   seg.UpdatedAt.Time,
			},
			MemberCount: memberCount,
		}
	}

	return result, total, nil
}

// GetByID returns a single segment with member count as domain object.
func (s *SegmentService) GetByID(ctx context.Context, id int64) (*SegmentWithCount, error) {
	seg, err := s.segmentRepo.FindByID(ctx, id)
	if err != nil {
		return nil, ErrSegmentNotFound
	}

	memberCount, err := s.memberRepo.CountBySegment(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("count members: %w", err)
	}

	return &SegmentWithCount{
		Segment: Segment{
			ID:          seg.ID,
			Name:        seg.Name,
			Description: seg.Description.String,
			CreatedBy:   seg.CreatedBy,
			CreatedAt:   seg.CreatedAt.Time,
			UpdatedAt:   seg.UpdatedAt.Time,
		},
		MemberCount: memberCount,
	}, nil
}

// CreateParams holds input for creating a segment.
type CreateParams struct {
	Name        string
	Description *string
	CreatedBy   int64
}

// Create creates a new segment and returns the domain Segment.
func (s *SegmentService) Create(ctx context.Context, params CreateParams) (*Segment, error) {
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

	seg, err := s.segmentRepo.Create(ctx, sqlc.CreateSegmentParams{
		Name:        params.Name,
		Description: desc,
		CreatedBy:   params.CreatedBy,
	})
	if err != nil {
		return nil, fmt.Errorf("create segment: %w", err)
	}

	return &Segment{
		ID:          seg.ID,
		Name:        seg.Name,
		Description: seg.Description.String,
		CreatedBy:   seg.CreatedBy,
		CreatedAt:   seg.CreatedAt.Time,
		UpdatedAt:   seg.UpdatedAt.Time,
	}, nil
}

// UpdateParams holds input for updating a segment.
type UpdateParams struct {
	ID          int64
	Name        *string
	Description *string
}

// Update updates an existing segment. Returns the updated domain Segment.
func (s *SegmentService) Update(ctx context.Context, params UpdateParams) (*Segment, error) {
	existing, err := s.segmentRepo.FindByID(ctx, params.ID)
	if err != nil {
		return nil, ErrSegmentNotFound
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
				return nil, fmt.Errorf("check name: %w", err)
			}
			if exists {
				return nil, ErrSegmentNameTaken
			}
			update.Name = *params.Name
		}
	}
	if params.Description != nil {
		update.Description = pgtype.Text{String: *params.Description, Valid: true}
	}

	if err := s.segmentRepo.Update(ctx, update); err != nil {
		return nil, fmt.Errorf("update segment: %w", err)
	}

	updated, err := s.segmentRepo.FindByID(ctx, params.ID)
	if err != nil {
		return nil, fmt.Errorf("fetch updated segment: %w", err)
	}

	return &Segment{
		ID:          updated.ID,
		Name:        updated.Name,
		Description: updated.Description.String,
		CreatedBy:   updated.CreatedBy,
		CreatedAt:   updated.CreatedAt.Time,
		UpdatedAt:   updated.UpdatedAt.Time,
	}, nil
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
