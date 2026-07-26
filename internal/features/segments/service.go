package segments

import (
	"context"
	"errors"
	"fmt"
	"log"

	"hello/internal/audit"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

// Domain errors.
var (
	ErrStaffNotFoundOrInactive = errors.New("staff account not found or inactive")
	ErrSegmentNotFound         = errors.New("segment not found")
	ErrSegmentNameTaken        = errors.New("segment name already exists")
	ErrSegmentHasMembers       = errors.New("cannot delete segment that has members")
)

// SegmentService provides business logic for segment management.
type SegmentService struct {
	segmentRepo  *repository.SegmentRepository
	memberRepo   *repository.SegmentMemberRepository
	staffRepo    *repository.StaffUserRepository
	auditService *audit.AuditService
}

// NewSegmentService creates a new SegmentService instance.
func NewSegmentService(
	segmentRepo *repository.SegmentRepository,
	memberRepo *repository.SegmentMemberRepository,
	staffRepo *repository.StaffUserRepository,
	auditService *audit.AuditService,
) *SegmentService {
	return &SegmentService{
		segmentRepo:  segmentRepo,
		memberRepo:   memberRepo,
		staffRepo:    staffRepo,
		auditService: auditService,
	}
}

// List returns a paginated list of segments with optional search.
func (s *SegmentService) List(ctx context.Context, page, limit int32, search string) ([]SegmentWithCount, int64, error) {
	offset := (page - 1) * limit

	var segments []Segment
	var err error

	if search != "" {
		rows, err := s.segmentRepo.Search(ctx, search, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("search segments: %w", err)
		}
		segments = make([]Segment, len(rows))
		for i, row := range rows {
			segments[i] = Segment{
				ID:          row.ID,
				Name:        row.Name,
				Description: row.Description.String,
				CreatedBy: Actor{
					ID:   row.CreatedBy,
					Name: row.CreatedByName,
				},
				CreatedAt: row.CreatedAt.Time,
				UpdatedAt: row.UpdatedAt.Time,
			}
		}
	} else {
		rows, err := s.segmentRepo.List(ctx, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("list segments: %w", err)
		}
		segments = make([]Segment, len(rows))
		for i, row := range rows {
			segments[i] = Segment{
				ID:          row.ID,
				Name:        row.Name,
				Description: row.Description.String,
				CreatedBy: Actor{
					ID:   row.CreatedBy,
					Name: row.CreatedByName,
				},
				CreatedAt: row.CreatedAt.Time,
				UpdatedAt: row.UpdatedAt.Time,
			}
		}
	}

	total, err := s.segmentRepo.Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count segments: %w", err)
	}

	result := make([]SegmentWithCount, len(segments))
	for i, seg := range segments {
		memberCount, _ := s.memberRepo.CountBySegment(ctx, seg.ID)
		result[i] = SegmentWithCount{
			Segment:     seg,
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

	actor, err := s.staffRepo.FindByID(ctx, seg.CreatedBy)
	actorName := ""
	if err == nil {
		actorName = actor.Name
	}

	return &SegmentWithCount{
		Segment: Segment{
			ID:          seg.ID,
			Name:        seg.Name,
			Description: seg.Description.String,
			CreatedBy: Actor{
				ID:   actor.ID,
				Name: actorName,
			},
			CreatedAt: seg.CreatedAt.Time,
			UpdatedAt: seg.UpdatedAt.Time,
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
	actorID, ok := audit.GetStaffID(ctx)
	if !ok {
		return nil, ErrStaffNotFoundOrInactive
	}
	_, err := s.staffRepo.FindByID(ctx, actorID)
	if err != nil {
		return nil, ErrStaffNotFoundOrInactive
	}

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

	if errLog := s.auditService.Log(ctx, audit.LogParams{
		Action:     audit.ACTION_SEGMENT_CREATE,
		EntityType: audit.ENTITY_TYPE_SEGMENT,
		EntityName: seg.Name,
		EntityID:   seg.ID,
		After:      seg,
	}); errLog != nil {
		log.Printf(
			"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
			audit.ACTION_SEGMENT_CREATE,
			audit.ENTITY_TYPE_SEGMENT,
			seg.ID,
			seg.Name,
			errLog,
		)
	}

	actor, err := s.staffRepo.FindByID(ctx, seg.CreatedBy)
	actorName := ""
	if err == nil {
		actorName = actor.Name
	}

	return &Segment{
		ID:          seg.ID,
		Name:        seg.Name,
		Description: seg.Description.String,
		CreatedBy: Actor{
			ID:   actor.ID,
			Name: actorName,
		},
		CreatedAt: seg.CreatedAt.Time,
		UpdatedAt: seg.UpdatedAt.Time,
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
	actorID, ok := audit.GetStaffID(ctx)
	if !ok {
		return nil, ErrStaffNotFoundOrInactive
	}
	_, err := s.staffRepo.FindByID(ctx, actorID)
	if err != nil {
		return nil, ErrStaffNotFoundOrInactive
	}

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

	if errLog := s.auditService.Log(ctx, audit.LogParams{
		Action:     audit.ACTION_SEGMENT_UPDATE,
		EntityType: audit.ENTITY_TYPE_SEGMENT,
		EntityName: updated.Name,
		EntityID:   updated.ID,
		Before:     existing,
		After:      updated,
	}); errLog != nil {
		log.Printf(
			"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
			audit.ACTION_SEGMENT_UPDATE,
			audit.ENTITY_TYPE_SEGMENT,
			updated.ID,
			updated.Name,
			errLog,
		)
	}

	actor, err := s.staffRepo.FindByID(ctx, updated.CreatedBy)
	actorName := ""
	if err == nil {
		actorName = actor.Name
	}

	return &Segment{
		ID:          updated.ID,
		Name:        updated.Name,
		Description: updated.Description.String,
		CreatedBy: Actor{
			ID:   actor.ID,
			Name: actorName,
		},
		CreatedAt: updated.CreatedAt.Time,
		UpdatedAt: updated.UpdatedAt.Time,
	}, nil
}

// Delete deletes a segment only if it has no members.
func (s *SegmentService) Delete(ctx context.Context, id int64) error {
	actorID, ok := audit.GetStaffID(ctx)
	if !ok {
		return ErrStaffNotFoundOrInactive
	}
	_, err := s.staffRepo.FindByID(ctx, actorID)
	if err != nil {
		return ErrStaffNotFoundOrInactive
	}

	existing, err := s.segmentRepo.FindByID(ctx, id)
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

	if err := s.segmentRepo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete segment: %w", err)
	}

	if errLog := s.auditService.Log(ctx, audit.LogParams{
		Action:     audit.ACTION_SEGMENT_DELETE,
		EntityType: audit.ENTITY_TYPE_SEGMENT,
		EntityName: existing.Name,
		EntityID:   existing.ID,
		Before:     existing,
	}); errLog != nil {
		log.Printf(
			"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
			audit.ACTION_SEGMENT_DELETE,
			audit.ENTITY_TYPE_SEGMENT,
			existing.ID,
			existing.Name,
			errLog,
		)
	}

	return nil
}
