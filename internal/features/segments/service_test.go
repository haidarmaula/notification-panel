package segments

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"hello/internal/authz"
	"hello/internal/database/sqlc"
	"hello/internal/features/segments/mocks"
	"hello/internal/middleware"
	"hello/internal/token"
)

// --------------------------------
// Helpers
// --------------------------------

type fixtureAuthority struct{}

func (fixtureAuthority) GetStaffAuthority(ctx context.Context, id int64) (sqlc.GetStaffAuthorityRow, error) {
	if id != 1 {
		return sqlc.GetStaffAuthorityRow{}, pgx.ErrNoRows
	}
	return sqlc.GetStaffAuthorityRow{ID: id, IsActive: true, RoleName: pgtype.Text{String: "OPERATOR", Valid: true}}, nil
}
func fixtureAuthorization() authz.Checker { return authz.New(fixtureAuthority{}) }

func dummyStaffUser(id int64) sqlc.StaffUser {
	return sqlc.StaffUser{
		ID:           id,
		RoleID:       1,
		Name:         "Test Staff",
		Email:        "test@example.com",
		PasswordHash: "hashed",
		IsActive:     true,
		CreatedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
		UpdatedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
}

func dummySegment(id int64, name, description string) sqlc.Segment {
	return sqlc.Segment{
		ID:          id,
		Name:        name,
		Description: pgtype.Text{String: description, Valid: true},
		CreatedBy:   1,
		CreatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
}

func ctxWithStaffID(id int64) context.Context {
	return context.WithValue(context.Background(), middleware.UserContextKey, &token.AccessClaims{StaffID: id})
}

var (
	errDB    = errors.New("database error")
	errFetch = errors.New("fetch error")
)

// --------------------------------
// List
// --------------------------------

func TestSegmentsService_List(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		page          int32
		limit         int32
		search        string
		mockSetup     func(*mocks.MockSegmentRepository, *mocks.MockSegmentMemberRepository)
		expectedError error
		verifyResult  func(t *testing.T, segments []SegmentWithCount, total int64)
	}

	tests := []testCase{
		{
			name:   "success without search",
			page:   1,
			limit:  10,
			search: "",
			mockSetup: func(repo *mocks.MockSegmentRepository, memberRepo *mocks.MockSegmentMemberRepository) {
				rows := []sqlc.ListSegmentsRow{
					{ID: 1, Name: "Seg A", CreatedBy: 1, CreatedByName: "Staff A"},
					{ID: 2, Name: "Seg B", CreatedBy: 1, CreatedByName: "Staff A"},
				}
				repo.EXPECT().List(mock.Anything, int32(0), int32(10)).Return(rows, nil).Once()
				repo.EXPECT().Count(mock.Anything).Return(int64(2), nil).Once()
				memberRepo.EXPECT().CountBySegmentIDs(mock.Anything, []int64{1, 2}).Return([]sqlc.CountSegmentMembersBySegmentIDsRow{
					{SegmentID: 2, MemberCount: 3},
					{SegmentID: 1, MemberCount: 5},
				}, nil).Once()
			},
			expectedError: nil,
			verifyResult: func(t *testing.T, segments []SegmentWithCount, total int64) {
				assert.Len(t, segments, 2)
				assert.Equal(t, int64(2), total)
				assert.Equal(t, int64(5), segments[0].MemberCount)
				assert.Equal(t, int64(3), segments[1].MemberCount)
			},
		},
		{
			name:   "success with search",
			page:   1,
			limit:  10,
			search: "test",
			mockSetup: func(repo *mocks.MockSegmentRepository, memberRepo *mocks.MockSegmentMemberRepository) {
				rows := []sqlc.SearchSegmentsRow{
					{ID: 1, Name: "Test Segment", CreatedBy: 1, CreatedByName: "Staff A"},
				}
				repo.EXPECT().CountSearch(mock.Anything, "test").Return(int64(7), nil).Once()
				repo.EXPECT().Search(mock.Anything, "test", int32(0), int32(10)).Return(rows, nil).Once()
				memberRepo.EXPECT().CountBySegmentIDs(mock.Anything, []int64{1}).Return(nil, nil).Once()
			},
			expectedError: nil,
			verifyResult: func(t *testing.T, segments []SegmentWithCount, total int64) {
				assert.Len(t, segments, 1)
				assert.Equal(t, int64(7), total)
				assert.Equal(t, "Test Segment", segments[0].Name)
				assert.Equal(t, int64(0), segments[0].MemberCount)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockSegmentRepo := mocks.NewMockSegmentRepository(t)
			mockMemberRepo := mocks.NewMockSegmentMemberRepository(t)
			mockStaffRepo := mocks.NewMockStaffUserRepository(t)
			mockAudit := mocks.NewMockAuditService(t)

			tc.mockSetup(mockSegmentRepo, mockMemberRepo)

			service := NewSegmentService(mockSegmentRepo, mockMemberRepo, mockStaffRepo, mockAudit, fixtureAuthorization())
			segments, total, err := service.List(ctxWithStaffID(1), tc.page, tc.limit, tc.search)

			assert.ErrorIs(t, err, tc.expectedError)
			if tc.verifyResult != nil {
				tc.verifyResult(t, segments, total)
			}
		})
	}
}

// --------------------------------
// List failures and empty page
// --------------------------------

func TestSegmentsService_ListFailuresAndEmptyPage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		search        string
		setup         func(*mocks.MockSegmentRepository, *mocks.MockSegmentMemberRepository)
		expectedError error
	}{
		{
			name: "list failure",
			setup: func(repo *mocks.MockSegmentRepository, members *mocks.MockSegmentMemberRepository) {
				repo.EXPECT().List(mock.Anything, int32(0), int32(10)).Return(nil, errDB).Once()
			}, expectedError: errDB,
		},
		{
			name: "count failure",
			setup: func(repo *mocks.MockSegmentRepository, members *mocks.MockSegmentMemberRepository) {
				repo.EXPECT().List(mock.Anything, int32(0), int32(10)).Return(nil, nil).Once()
				repo.EXPECT().Count(mock.Anything).Return(int64(0), errDB).Once()
			}, expectedError: errDB,
		},
		{
			name: "search failure", search: "test",
			setup: func(repo *mocks.MockSegmentRepository, members *mocks.MockSegmentMemberRepository) {
				repo.EXPECT().Search(mock.Anything, "test", int32(0), int32(10)).Return(nil, errDB).Once()
			}, expectedError: errDB,
		},
		{
			name: "filtered count failure", search: "test",
			setup: func(repo *mocks.MockSegmentRepository, members *mocks.MockSegmentMemberRepository) {
				repo.EXPECT().Search(mock.Anything, "test", int32(0), int32(10)).Return(nil, nil).Once()
				repo.EXPECT().CountSearch(mock.Anything, "test").Return(int64(0), errDB).Once()
			}, expectedError: errDB,
		},
		{
			name: "batch count failure",
			setup: func(repo *mocks.MockSegmentRepository, members *mocks.MockSegmentMemberRepository) {
				repo.EXPECT().List(mock.Anything, int32(0), int32(10)).Return([]sqlc.ListSegmentsRow{{ID: 1}}, nil).Once()
				repo.EXPECT().Count(mock.Anything).Return(int64(1), nil).Once()
				members.EXPECT().CountBySegmentIDs(mock.Anything, []int64{1}).Return(nil, errDB).Once()
			}, expectedError: errDB,
		},
		{
			name: "empty page skips member query",
			setup: func(repo *mocks.MockSegmentRepository, members *mocks.MockSegmentMemberRepository) {
				repo.EXPECT().List(mock.Anything, int32(0), int32(10)).Return(nil, nil).Once()
				repo.EXPECT().Count(mock.Anything).Return(int64(0), nil).Once()
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := mocks.NewMockSegmentRepository(t)
			members := mocks.NewMockSegmentMemberRepository(t)
			staff := mocks.NewMockStaffUserRepository(t)
			audit := mocks.NewMockAuditService(t)
			tc.setup(repo, members)
			service := NewSegmentService(repo, members, staff, audit, fixtureAuthorization())
			segments, total, err := service.List(ctxWithStaffID(1), 1, 10, tc.search)
			require.ErrorIs(t, err, tc.expectedError)
			require.Empty(t, segments)
			require.Zero(t, total)
		})
	}
}

// --------------------------------
// GetByID
// --------------------------------

func TestSegmentsService_GetByID(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		id            int64
		mockSetup     func(*mocks.MockSegmentRepository, *mocks.MockStaffUserRepository, *mocks.MockSegmentMemberRepository)
		expectedError error
		verifyResult  func(t *testing.T, result *SegmentWithCount)
	}

	tests := []testCase{
		{
			name: "success",
			id:   1,
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, memberRepo *mocks.MockSegmentMemberRepository) {
				seg := dummySegment(1, "Test", "desc")
				staffRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(dummyStaffUser(1), nil).Once()
				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(seg, nil).Once()
				memberRepo.EXPECT().CountBySegment(mock.Anything, int64(1)).Return(int64(10), nil).Once()
			},
			expectedError: nil,
			verifyResult: func(t *testing.T, result *SegmentWithCount) {
				assert.NotNil(t, result)
				assert.Equal(t, int64(1), result.ID)
				assert.Equal(t, "Test", result.Name)
				assert.Equal(t, int64(10), result.MemberCount)
				assert.Equal(t, "Test Staff", result.CreatedBy.Name)
			},
		},
		{
			name: "segment not found",
			id:   999,
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, memberRepo *mocks.MockSegmentMemberRepository) {
				segmentRepo.EXPECT().FindByID(mock.Anything, int64(999)).Return(sqlc.Segment{}, pgx.ErrNoRows).Once()
			},
			expectedError: ErrSegmentNotFound,
			verifyResult: func(t *testing.T, result *SegmentWithCount) {
				assert.Nil(t, result)
			},
		},
		{
			name: "creator not found (name empty)",
			id:   1,
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, memberRepo *mocks.MockSegmentMemberRepository) {
				seg := dummySegment(1, "Test", "desc")
				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(seg, nil).Once()
				staffRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(sqlc.StaffUser{}, pgx.ErrNoRows).Once()
				memberRepo.EXPECT().CountBySegment(mock.Anything, int64(1)).Return(int64(0), nil).Once()
			},
			expectedError: nil,
			verifyResult: func(t *testing.T, result *SegmentWithCount) {
				assert.NotNil(t, result)
				assert.Equal(t, "", result.CreatedBy.Name)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockSegmentRepo := mocks.NewMockSegmentRepository(t)
			mockMemberRepo := mocks.NewMockSegmentMemberRepository(t)
			mockStaffRepo := mocks.NewMockStaffUserRepository(t)
			mockAudit := mocks.NewMockAuditService(t)

			tc.mockSetup(mockSegmentRepo, mockStaffRepo, mockMemberRepo)

			service := NewSegmentService(mockSegmentRepo, mockMemberRepo, mockStaffRepo, mockAudit, fixtureAuthorization())
			result, err := service.GetByID(ctxWithStaffID(1), tc.id)

			assert.ErrorIs(t, err, tc.expectedError)
			if tc.verifyResult != nil {
				tc.verifyResult(t, result)
			}
		})
	}
}

// --------------------------------
// Create
// --------------------------------

func TestSegmentsService_Create(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		params        CreateParams
		ctx           context.Context
		mockSetup     func(*mocks.MockSegmentRepository, *mocks.MockStaffUserRepository, *mocks.MockAuditService)
		expectedError error
		verifyResult  func(t *testing.T, segment *Segment)
	}

	tests := []testCase{
		{
			name: "successful creation",
			params: CreateParams{
				Name:        "Premium Users",
				Description: new("Users with premium subscription"),
			},
			ctx: ctxWithStaffID(1),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {
				staffRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(dummyStaffUser(1), nil).Once()
				segmentRepo.EXPECT().ExistsByName(mock.Anything, "Premium Users").Return(false, nil).Once()
				segmentRepo.EXPECT().Create(mock.Anything, mock.MatchedBy(func(p sqlc.CreateSegmentParams) bool {
					return p.Name == "Premium Users" && p.Description.String == "Users with premium subscription"
				})).Return(dummySegment(1, "Premium Users", "Users with premium subscription"), nil).Once()
				auditService.EXPECT().Log(mock.Anything, mock.Anything).Return(nil).Once()
			},
			expectedError: nil,
			verifyResult: func(t *testing.T, segment *Segment) {
				assert.NotNil(t, segment)
				assert.Equal(t, int64(1), segment.ID)
				assert.Equal(t, "Premium Users", segment.Name)
				assert.Equal(t, "Users with premium subscription", segment.Description)
			},
		},
		{
			name: "missing staff ID in context",
			params: CreateParams{
				Name: "Premium Users",
			},
			ctx: context.Background(),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {
			},
			expectedError: authz.ErrUnauthenticated,
			verifyResult: func(t *testing.T, segment *Segment) {
				assert.Nil(t, segment)
			},
		},
		{
			name: "staff not found",
			params: CreateParams{
				Name: "Premium Users",
			},
			ctx: ctxWithStaffID(999),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {

			},
			expectedError: authz.ErrUnauthenticated,
			verifyResult: func(t *testing.T, segment *Segment) {
				assert.Nil(t, segment)
			},
		},
		{
			name: "segment name already exists",
			params: CreateParams{
				Name: "Premium Users",
			},
			ctx: ctxWithStaffID(1),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {

				segmentRepo.EXPECT().ExistsByName(mock.Anything, "Premium Users").Return(true, nil).Once()
			},
			expectedError: ErrSegmentNameTaken,
			verifyResult: func(t *testing.T, segment *Segment) {
				assert.Nil(t, segment)
			},
		},
		{
			name: "check name existence fails (DB error)",
			params: CreateParams{
				Name: "Premium Users",
			},
			ctx: ctxWithStaffID(1),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {

				segmentRepo.EXPECT().ExistsByName(mock.Anything, "Premium Users").Return(false, errDB).Once()
			},
			expectedError: errDB,
			verifyResult: func(t *testing.T, segment *Segment) {
				assert.Nil(t, segment)
			},
		},
		{
			name: "segment creation fails (DB error)",
			params: CreateParams{
				Name:        "Premium Users",
				Description: new("desc"),
			},
			ctx: ctxWithStaffID(1),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {

				segmentRepo.EXPECT().ExistsByName(mock.Anything, "Premium Users").Return(false, nil).Once()
				segmentRepo.EXPECT().Create(mock.Anything, mock.Anything).Return(sqlc.Segment{}, errDB).Once()
			},
			expectedError: errDB,
			verifyResult: func(t *testing.T, segment *Segment) {
				assert.Nil(t, segment)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockSegmentRepo := mocks.NewMockSegmentRepository(t)
			mockMemberRepo := mocks.NewMockSegmentMemberRepository(t)
			mockStaffRepo := mocks.NewMockStaffUserRepository(t)
			mockAuditService := mocks.NewMockAuditService(t)

			tc.mockSetup(mockSegmentRepo, mockStaffRepo, mockAuditService)

			service := NewSegmentService(mockSegmentRepo, mockMemberRepo, mockStaffRepo, mockAuditService, fixtureAuthorization())

			segment, err := service.Create(tc.ctx, tc.params)

			assert.ErrorIs(t, err, tc.expectedError)
			if tc.verifyResult != nil {
				tc.verifyResult(t, segment)
			}
		})
	}
}

// --------------------------------
// Update
// --------------------------------

func TestSegmentsService_Update(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		params        UpdateParams
		ctx           context.Context
		mockSetup     func(*mocks.MockSegmentRepository, *mocks.MockStaffUserRepository, *mocks.MockAuditService)
		expectedError error
		verifyResult  func(t *testing.T, segment *Segment)
	}

	tests := []testCase{
		{
			name: "successful update (name and description)",
			params: UpdateParams{
				ID:          1,
				Name:        new("Updated Name"),
				Description: new("Updated description"),
			},
			ctx: ctxWithStaffID(1),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {
				existing := dummySegment(1, "Old Name", "Old desc")
				updated := dummySegment(1, "Updated Name", "Updated description")

				staffRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(dummyStaffUser(1), nil).Once()
				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(existing, nil).Once()
				segmentRepo.EXPECT().ExistsByName(mock.Anything, "Updated Name").Return(false, nil).Once()
				segmentRepo.EXPECT().Update(mock.Anything, mock.MatchedBy(func(p sqlc.UpdateSegmentParams) bool {
					return p.ID == 1 && p.Name == "Updated Name" && p.Description.String == "Updated description"
				})).Return(nil).Once()
				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(updated, nil).Once()
				auditService.EXPECT().Log(mock.Anything, mock.Anything).Return(nil).Once()
			},
			expectedError: nil,
			verifyResult: func(t *testing.T, segment *Segment) {
				assert.NotNil(t, segment)
				assert.Equal(t, "Updated Name", segment.Name)
				assert.Equal(t, "Updated description", segment.Description)
			},
		},
		{
			name: "update name only",
			params: UpdateParams{
				ID:   1,
				Name: new("New Name Only"),
			},
			ctx: ctxWithStaffID(1),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {
				existing := dummySegment(1, "Old Name", "Old desc")
				updated := dummySegment(1, "New Name Only", "Old desc")

				staffRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(dummyStaffUser(1), nil).Once()
				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(existing, nil).Once()
				segmentRepo.EXPECT().ExistsByName(mock.Anything, "New Name Only").Return(false, nil).Once()
				segmentRepo.EXPECT().Update(mock.Anything, mock.MatchedBy(func(p sqlc.UpdateSegmentParams) bool {
					return p.ID == 1 && p.Name == "New Name Only" && p.Description.String == "Old desc"
				})).Return(nil).Once()
				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(updated, nil).Once()
				auditService.EXPECT().Log(mock.Anything, mock.Anything).Return(nil).Once()
			},
			expectedError: nil,
			verifyResult: func(t *testing.T, segment *Segment) {
				assert.NotNil(t, segment)
				assert.Equal(t, "New Name Only", segment.Name)
				assert.Equal(t, "Old desc", segment.Description)
			},
		},
		{
			name: "update description only",
			params: UpdateParams{
				ID:          1,
				Description: new("New description only"),
			},
			ctx: ctxWithStaffID(1),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {
				existing := dummySegment(1, "Old Name", "Old desc")
				updated := dummySegment(1, "Old Name", "New description only")

				staffRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(dummyStaffUser(1), nil).Once()
				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(existing, nil).Once()
				// No ExistsByName because name not changed
				segmentRepo.EXPECT().Update(mock.Anything, mock.MatchedBy(func(p sqlc.UpdateSegmentParams) bool {
					return p.ID == 1 && p.Name == "Old Name" && p.Description.String == "New description only"
				})).Return(nil).Once()
				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(updated, nil).Once()
				auditService.EXPECT().Log(mock.Anything, mock.Anything).Return(nil).Once()
			},
			expectedError: nil,
			verifyResult: func(t *testing.T, segment *Segment) {
				assert.NotNil(t, segment)
				assert.Equal(t, "Old Name", segment.Name)
				assert.Equal(t, "New description only", segment.Description)
			},
		},
		{
			name: "update with same name (no duplicate check)",
			params: UpdateParams{
				ID:          1,
				Name:        new("Old Name"),
				Description: new("Updated description"),
			},
			ctx: ctxWithStaffID(1),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {
				existing := dummySegment(1, "Old Name", "Old desc")
				updated := dummySegment(1, "Old Name", "Updated description")

				staffRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(dummyStaffUser(1), nil).Once()
				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(existing, nil).Once()
				// No ExistsByName
				segmentRepo.EXPECT().Update(mock.Anything, mock.MatchedBy(func(p sqlc.UpdateSegmentParams) bool {
					return p.ID == 1 && p.Name == "Old Name" && p.Description.String == "Updated description"
				})).Return(nil).Once()
				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(updated, nil).Once()
				auditService.EXPECT().Log(mock.Anything, mock.Anything).Return(nil).Once()
			},
			expectedError: nil,
			verifyResult: func(t *testing.T, segment *Segment) {
				assert.NotNil(t, segment)
				assert.Equal(t, "Old Name", segment.Name)
				assert.Equal(t, "Updated description", segment.Description)
			},
		},
		{
			name: "missing staff ID in context",
			params: UpdateParams{
				ID:   1,
				Name: new("New Name"),
			},
			ctx: context.Background(),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {
			},
			expectedError: authz.ErrUnauthenticated,
			verifyResult: func(t *testing.T, segment *Segment) {
				assert.Nil(t, segment)
			},
		},
		{
			name: "staff not found",
			params: UpdateParams{
				ID:   1,
				Name: new("New Name"),
			},
			ctx: ctxWithStaffID(999),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {

			},
			expectedError: authz.ErrUnauthenticated,
			verifyResult: func(t *testing.T, segment *Segment) {
				assert.Nil(t, segment)
			},
		},
		{
			name: "segment not found",
			params: UpdateParams{
				ID:   999,
				Name: new("New Name"),
			},
			ctx: ctxWithStaffID(1),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {

				segmentRepo.EXPECT().FindByID(mock.Anything, int64(999)).Return(sqlc.Segment{}, pgx.ErrNoRows).Once()
			},
			expectedError: ErrSegmentNotFound,
			verifyResult: func(t *testing.T, segment *Segment) {
				assert.Nil(t, segment)
			},
		},
		{
			name: "name already exists",
			params: UpdateParams{
				ID:   1,
				Name: new("Taken Name"),
			},
			ctx: ctxWithStaffID(1),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {
				existing := dummySegment(1, "Old Name", "Old desc")

				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(existing, nil).Once()
				segmentRepo.EXPECT().ExistsByName(mock.Anything, "Taken Name").Return(true, nil).Once()
			},
			expectedError: ErrSegmentNameTaken,
			verifyResult: func(t *testing.T, segment *Segment) {
				assert.Nil(t, segment)
			},
		},
		{
			name: "check name existence fails (DB error)",
			params: UpdateParams{
				ID:   1,
				Name: new("New Name"),
			},
			ctx: ctxWithStaffID(1),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {
				existing := dummySegment(1, "Old Name", "Old desc")

				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(existing, nil).Once()
				segmentRepo.EXPECT().ExistsByName(mock.Anything, "New Name").Return(false, errDB).Once()
			},
			expectedError: errDB,
			verifyResult: func(t *testing.T, segment *Segment) {
				assert.Nil(t, segment)
			},
		},
		{
			name: "update fails (DB error)",
			params: UpdateParams{
				ID:   1,
				Name: new("New Name"),
			},
			ctx: ctxWithStaffID(1),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {
				existing := dummySegment(1, "Old Name", "Old desc")

				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(existing, nil).Once()
				segmentRepo.EXPECT().ExistsByName(mock.Anything, "New Name").Return(false, nil).Once()
				segmentRepo.EXPECT().Update(mock.Anything, mock.Anything).Return(errDB).Once()
			},
			expectedError: errDB,
			verifyResult: func(t *testing.T, segment *Segment) {
				assert.Nil(t, segment)
			},
		},
		{
			name: "fetch updated segment fails",
			params: UpdateParams{
				ID:   1,
				Name: new("New Name"),
			},
			ctx: ctxWithStaffID(1),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {
				existing := dummySegment(1, "Old Name", "Old desc")

				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(existing, nil).Once()
				segmentRepo.EXPECT().ExistsByName(mock.Anything, "New Name").Return(false, nil).Once()
				segmentRepo.EXPECT().Update(mock.Anything, mock.Anything).Return(nil).Once()
				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(sqlc.Segment{}, errFetch).Once()
			},
			expectedError: errFetch,
			verifyResult: func(t *testing.T, segment *Segment) {
				assert.Nil(t, segment)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockSegmentRepo := mocks.NewMockSegmentRepository(t)
			mockMemberRepo := mocks.NewMockSegmentMemberRepository(t) // not used in update
			mockStaffRepo := mocks.NewMockStaffUserRepository(t)
			mockAuditService := mocks.NewMockAuditService(t)

			tc.mockSetup(mockSegmentRepo, mockStaffRepo, mockAuditService)

			service := NewSegmentService(mockSegmentRepo, mockMemberRepo, mockStaffRepo, mockAuditService, fixtureAuthorization())

			segment, err := service.Update(tc.ctx, tc.params)

			assert.ErrorIs(t, err, tc.expectedError)
			if tc.verifyResult != nil {
				tc.verifyResult(t, segment)
			}
		})
	}
}

// --------------------------------
// Delete
// --------------------------------

func TestSegmentsService_Delete(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		segmentID     int64
		ctx           context.Context
		mockSetup     func(*mocks.MockSegmentRepository, *mocks.MockSegmentMemberRepository, *mocks.MockStaffUserRepository, *mocks.MockAuditService)
		expectedError error
	}

	tests := []testCase{
		{
			name:      "successful delete",
			segmentID: 1,
			ctx:       ctxWithStaffID(1),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, memberRepo *mocks.MockSegmentMemberRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {
				existing := dummySegment(1, "Test", "desc")

				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(existing, nil).Once()
				memberRepo.EXPECT().CountBySegment(mock.Anything, int64(1)).Return(int64(0), nil).Once()
				segmentRepo.EXPECT().Delete(mock.Anything, int64(1)).Return(nil).Once()
				auditService.EXPECT().Log(mock.Anything, mock.Anything).Return(nil).Once()
			},
			expectedError: nil,
		},
		{
			name:      "missing staff ID in context",
			segmentID: 1,
			ctx:       context.Background(),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, memberRepo *mocks.MockSegmentMemberRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {
			},
			expectedError: authz.ErrUnauthenticated,
		},
		{
			name:      "staff not found",
			segmentID: 1,
			ctx:       ctxWithStaffID(999),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, memberRepo *mocks.MockSegmentMemberRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {

			},
			expectedError: authz.ErrUnauthenticated,
		},
		{
			name:      "segment not found",
			segmentID: 999,
			ctx:       ctxWithStaffID(1),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, memberRepo *mocks.MockSegmentMemberRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {

				segmentRepo.EXPECT().FindByID(mock.Anything, int64(999)).Return(sqlc.Segment{}, pgx.ErrNoRows).Once()
			},
			expectedError: ErrSegmentNotFound,
		},
		{
			name:      "segment has members",
			segmentID: 1,
			ctx:       ctxWithStaffID(1),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, memberRepo *mocks.MockSegmentMemberRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {
				existing := dummySegment(1, "Test", "desc")

				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(existing, nil).Once()
				memberRepo.EXPECT().CountBySegment(mock.Anything, int64(1)).Return(int64(5), nil).Once()
			},
			expectedError: ErrSegmentHasMembers,
		},
		{
			name:      "count members fails (DB error)",
			segmentID: 1,
			ctx:       ctxWithStaffID(1),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, memberRepo *mocks.MockSegmentMemberRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {
				existing := dummySegment(1, "Test", "desc")

				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(existing, nil).Once()
				memberRepo.EXPECT().CountBySegment(mock.Anything, int64(1)).Return(int64(0), errDB).Once()
			},
			expectedError: errDB,
		},
		{
			name:      "delete fails (DB error)",
			segmentID: 1,
			ctx:       ctxWithStaffID(1),
			mockSetup: func(segmentRepo *mocks.MockSegmentRepository, memberRepo *mocks.MockSegmentMemberRepository, staffRepo *mocks.MockStaffUserRepository, auditService *mocks.MockAuditService) {
				existing := dummySegment(1, "Test", "desc")

				segmentRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(existing, nil).Once()
				memberRepo.EXPECT().CountBySegment(mock.Anything, int64(1)).Return(int64(0), nil).Once()
				segmentRepo.EXPECT().Delete(mock.Anything, int64(1)).Return(errDB).Once()
			},
			expectedError: errDB,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockSegmentRepo := mocks.NewMockSegmentRepository(t)
			mockMemberRepo := mocks.NewMockSegmentMemberRepository(t)
			mockStaffRepo := mocks.NewMockStaffUserRepository(t)
			mockAuditService := mocks.NewMockAuditService(t)

			tc.mockSetup(mockSegmentRepo, mockMemberRepo, mockStaffRepo, mockAuditService)

			service := NewSegmentService(mockSegmentRepo, mockMemberRepo, mockStaffRepo, mockAuditService, fixtureAuthorization())

			err := service.Delete(tc.ctx, tc.segmentID)

			assert.ErrorIs(t, err, tc.expectedError)
		})
	}
}
