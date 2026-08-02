package staff

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"hello/internal/database/sqlc"
	"hello/internal/features/staff/mocks"
	"hello/internal/middleware"
	"hello/internal/token"
)

// ============================================
// HELPERS
// ============================================

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

func dummyRole() sqlc.Role {
	return sqlc.Role{
		ID:   1,
		Name: "ADMIN",
	}
}

func ctxWithStaffID(id int64) context.Context {
	return context.WithValue(context.Background(), middleware.UserContextKey, &token.AccessClaims{StaffID: id})
}

// ============================================
// CREATE
// ============================================

func TestStaffService_Create(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		params        CreateStaffParams
		mockSetup     func(*mocks.MockStaffUserRepository, *mocks.MockRoleRepository, *mocks.MockAuditService)
		expectedError error
		verifyResult  func(*testing.T, *Staff)
	}

	tests := []testCase{
		{
			name: "successful creation",
			params: CreateStaffParams{
				Role:     "ADMIN",
				Name:     "New Staff",
				Email:    "new@example.com",
				Password: "password123",
			},
			mockSetup: func(repo *mocks.MockStaffUserRepository, roleRepo *mocks.MockRoleRepository, audit *mocks.MockAuditService) {
				repo.EXPECT().ExistsByEmail(mock.Anything, "new@example.com").Return(false, nil).Once()
				roleRepo.EXPECT().FindByName(mock.Anything, "ADMIN").Return(dummyRole(), nil).Once()
				repo.EXPECT().Create(mock.Anything, mock.MatchedBy(func(p sqlc.CreateStaffUserParams) bool {
					return p.Name == "New Staff" && p.Email == "new@example.com"
				})).Return(dummyStaffUser(1), nil).Once()
				audit.EXPECT().Log(mock.Anything, mock.Anything).Return(nil).Once()
			},
			expectedError: nil,
			verifyResult: func(t *testing.T, staff *Staff) {
				assert.NotNil(t, staff)
				assert.Equal(t, int64(1), staff.ID)
				assert.Equal(t, "Test Staff", staff.Name)
				assert.Equal(t, "ADMIN", staff.RoleName)
			},
		},
		{
			name: "email already registered",
			params: CreateStaffParams{
				Email: "existing@example.com",
			},
			mockSetup: func(repo *mocks.MockStaffUserRepository, roleRepo *mocks.MockRoleRepository, audit *mocks.MockAuditService) {
				repo.EXPECT().ExistsByEmail(mock.Anything, "existing@example.com").Return(true, nil).Once()
			},
			expectedError: ErrEmailAlreadyRegistered,
			verifyResult: func(t *testing.T, staff *Staff) {
				assert.Nil(t, staff)
			},
		},
		{
			name: "invalid role",
			params: CreateStaffParams{
				Role:  "INVALID",
				Email: "new@example.com",
			},
			mockSetup: func(repo *mocks.MockStaffUserRepository, roleRepo *mocks.MockRoleRepository, audit *mocks.MockAuditService) {
				repo.EXPECT().ExistsByEmail(mock.Anything, "new@example.com").Return(false, nil).Once()
				roleRepo.EXPECT().FindByName(mock.Anything, "INVALID").Return(sqlc.Role{}, errors.New("not found")).Once()
			},
			expectedError: ErrInvalidRole,
			verifyResult: func(t *testing.T, staff *Staff) {
				assert.Nil(t, staff)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockStaffRepo := mocks.NewMockStaffUserRepository(t)
			mockRoleRepo := mocks.NewMockRoleRepository(t)
			mockAudit := mocks.NewMockAuditService(t)

			tc.mockSetup(mockStaffRepo, mockRoleRepo, mockAudit)

			service := NewStaffService(mockStaffRepo, mockRoleRepo, mockAudit)
			staff, err := service.Create(context.Background(), tc.params)

			assert.ErrorIs(t, err, tc.expectedError)
			if tc.verifyResult != nil {
				tc.verifyResult(t, staff)
			}
		})
	}
}

// ============================================
// GET BY ID
// ============================================

func TestStaffService_GetByID(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		id            int64
		mockSetup     func(*mocks.MockStaffUserRepository, *mocks.MockRoleRepository)
		expectedError error
	}

	tests := []testCase{
		{
			name: "success",
			id:   1,
			mockSetup: func(repo *mocks.MockStaffUserRepository, roleRepo *mocks.MockRoleRepository) {
				repo.EXPECT().FindByID(mock.Anything, int64(1)).Return(dummyStaffUser(1), nil).Once()
				roleRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(dummyRole(), nil).Once()
			},
			expectedError: nil,
		},
		{
			name: "not found",
			id:   999,
			mockSetup: func(repo *mocks.MockStaffUserRepository, roleRepo *mocks.MockRoleRepository) {
				repo.EXPECT().FindByID(mock.Anything, int64(999)).Return(sqlc.StaffUser{}, errors.New("not found")).Once()
			},
			expectedError: ErrStaffNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockStaffRepo := mocks.NewMockStaffUserRepository(t)
			mockRoleRepo := mocks.NewMockRoleRepository(t)
			mockAudit := mocks.NewMockAuditService(t)

			tc.mockSetup(mockStaffRepo, mockRoleRepo)

			service := NewStaffService(mockStaffRepo, mockRoleRepo, mockAudit)
			staff, err := service.GetByID(context.Background(), tc.id)

			assert.ErrorIs(t, err, tc.expectedError)
			if err == nil {
				assert.NotNil(t, staff)
				assert.Equal(t, tc.id, staff.ID)
			} else {
				assert.Nil(t, staff)
			}
		})
	}
}

// ============================================
// LIST
// ============================================

func TestStaffService_List(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		page          int32
		limit         int32
		search        string
		mockSetup     func(*mocks.MockStaffUserRepository)
		expectedError error
		verifyResult  func(*testing.T, []Staff, int64)
	}

	tests := []testCase{
		{
			name:   "success without search",
			page:   1,
			limit:  10,
			search: "",
			mockSetup: func(repo *mocks.MockStaffUserRepository) {
				rows := []sqlc.ListStaffUsersRow{
					{ID: 1, Name: "A", RoleName: "ADMIN"},
					{ID: 2, Name: "B", RoleName: "ADMIN"},
				}
				repo.EXPECT().Count(mock.Anything).Return(int64(2), nil).Once()
				repo.EXPECT().List(mock.Anything, int32(0), int32(10)).Return(rows, nil).Once()
			},
			expectedError: nil,
			verifyResult: func(t *testing.T, staffs []Staff, total int64) {
				assert.Len(t, staffs, 2)
				assert.Equal(t, int64(2), total)
			},
		},
		{
			name:   "success with search",
			page:   1,
			limit:  10,
			search: "test",
			mockSetup: func(repo *mocks.MockStaffUserRepository) {
				rows := []sqlc.SearchStaffUsersRow{
					{ID: 1, Name: "Test User", RoleName: "ADMIN"},
				}
				repo.EXPECT().Count(mock.Anything).Return(int64(1), nil).Once()
				repo.EXPECT().Search(mock.Anything, "test", int32(0), int32(10)).Return(rows, nil).Once()
			},
			expectedError: nil,
			verifyResult: func(t *testing.T, staffs []Staff, total int64) {
				assert.Len(t, staffs, 1)
				assert.Equal(t, int64(1), total)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockStaffRepo := mocks.NewMockStaffUserRepository(t)
			mockRoleRepo := mocks.NewMockRoleRepository(t)
			mockAudit := mocks.NewMockAuditService(t)

			tc.mockSetup(mockStaffRepo)

			service := NewStaffService(mockStaffRepo, mockRoleRepo, mockAudit)
			staffs, total, err := service.List(context.Background(), tc.page, tc.limit, tc.search)

			assert.ErrorIs(t, err, tc.expectedError)
			if tc.verifyResult != nil {
				tc.verifyResult(t, staffs, total)
			}
		})
	}
}

// ============================================
// UPDATE
// ============================================

func TestStaffService_Update(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		params        UpdateStaffParams
		mockSetup     func(*mocks.MockStaffUserRepository, *mocks.MockRoleRepository, *mocks.MockAuditService)
		expectedError error
		verifyResult  func(*testing.T, *Staff)
	}

	tests := []testCase{
		{
			name: "successful update",
			params: UpdateStaffParams{
				ID:    1,
				Role:  "ADMIN",
				Name:  "Updated",
				Email: "updated@example.com",
			},
			mockSetup: func(repo *mocks.MockStaffUserRepository, roleRepo *mocks.MockRoleRepository, audit *mocks.MockAuditService) {
				existing := dummyStaffUser(1)
				updated := existing
				updated.Name = "Updated"
				updated.Email = "updated@example.com"

				repo.EXPECT().FindByID(mock.Anything, int64(1)).Return(existing, nil).Once()
				roleRepo.EXPECT().FindByName(mock.Anything, "ADMIN").Return(dummyRole(), nil).Once()
				repo.EXPECT().ExistsByEmail(mock.Anything, "updated@example.com").Return(false, nil).Once()
				repo.EXPECT().Update(mock.Anything, mock.MatchedBy(func(p sqlc.UpdateStaffUserParams) bool {
					return p.Name == "Updated" && p.Email == "updated@example.com"
				})).Return(nil).Once()
				repo.EXPECT().FindByID(mock.Anything, int64(1)).Return(updated, nil).Once()
				roleRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(dummyRole(), nil).Once()
				audit.EXPECT().Log(mock.Anything, mock.Anything).Return(nil).Once()
			},
			expectedError: nil,
			verifyResult: func(t *testing.T, staff *Staff) {
				assert.NotNil(t, staff)
				assert.Equal(t, "Updated", staff.Name)
				assert.Equal(t, "updated@example.com", staff.Email)
			},
		},
		{
			name: "email already used",
			params: UpdateStaffParams{
				ID:    1,
				Email: "taken@example.com",
			},
			mockSetup: func(repo *mocks.MockStaffUserRepository, roleRepo *mocks.MockRoleRepository, audit *mocks.MockAuditService) {
				repo.EXPECT().FindByID(mock.Anything, int64(1)).Return(dummyStaffUser(1), nil).Once()
				repo.EXPECT().ExistsByEmail(mock.Anything, "taken@example.com").Return(true, nil).Once()
			},
			expectedError: ErrEmailAlreadyUsed,
			verifyResult: func(t *testing.T, staff *Staff) {
				assert.Nil(t, staff)
			},
		},
		{
			name: "invalid role",
			params: UpdateStaffParams{
				ID:   1,
				Role: "INVALID",
			},
			mockSetup: func(repo *mocks.MockStaffUserRepository, roleRepo *mocks.MockRoleRepository, audit *mocks.MockAuditService) {
				repo.EXPECT().FindByID(mock.Anything, int64(1)).Return(dummyStaffUser(1), nil).Once()
				roleRepo.EXPECT().FindByName(mock.Anything, "INVALID").Return(sqlc.Role{}, errors.New("not found")).Once()
			},
			expectedError: ErrInvalidRole,
			verifyResult: func(t *testing.T, staff *Staff) {
				assert.Nil(t, staff)
			},
		},
		{
			name: "staff not found",
			params: UpdateStaffParams{
				ID: 999,
			},
			mockSetup: func(repo *mocks.MockStaffUserRepository, roleRepo *mocks.MockRoleRepository, audit *mocks.MockAuditService) {
				repo.EXPECT().FindByID(mock.Anything, int64(999)).Return(sqlc.StaffUser{}, errors.New("not found")).Once()
			},
			expectedError: ErrStaffNotFound,
			verifyResult: func(t *testing.T, staff *Staff) {
				assert.Nil(t, staff)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockStaffRepo := mocks.NewMockStaffUserRepository(t)
			mockRoleRepo := mocks.NewMockRoleRepository(t)
			mockAudit := mocks.NewMockAuditService(t)

			tc.mockSetup(mockStaffRepo, mockRoleRepo, mockAudit)

			service := NewStaffService(mockStaffRepo, mockRoleRepo, mockAudit)
			staff, err := service.Update(context.Background(), tc.params)

			assert.ErrorIs(t, err, tc.expectedError)
			if tc.verifyResult != nil {
				tc.verifyResult(t, staff)
			}
		})
	}
}

// ============================================
// UPDATE STATUS
// ============================================

func TestStaffService_UpdateStatus(t *testing.T) {
	t.Parallel()

	t.Run("successful status update", func(t *testing.T) {
		t.Parallel()

		mockStaffRepo := mocks.NewMockStaffUserRepository(t)
		mockRoleRepo := mocks.NewMockRoleRepository(t)
		mockAudit := mocks.NewMockAuditService(t)

		id := int64(1)
		before := dummyStaffUser(id)
		after := before
		after.IsActive = false

		mockStaffRepo.EXPECT().FindByID(mock.Anything, id).Return(before, nil).Once()
		mockStaffRepo.EXPECT().UpdateStatus(mock.Anything, sqlc.UpdateStaffStatusParams{ID: id, IsActive: false}).Return(nil).Once()
		mockStaffRepo.EXPECT().FindByID(mock.Anything, id).Return(after, nil).Once()
		mockRoleRepo.EXPECT().FindByID(mock.Anything, int64(1)).Return(dummyRole(), nil).Once()
		mockAudit.EXPECT().Log(mock.Anything, mock.Anything).Return(nil).Once()

		service := NewStaffService(mockStaffRepo, mockRoleRepo, mockAudit)
		staff, err := service.UpdateStatus(context.Background(), id, false)

		assert.NoError(t, err)
		assert.False(t, staff.IsActive)
	})

	t.Run("staff not found", func(t *testing.T) {
		t.Parallel()

		mockStaffRepo := mocks.NewMockStaffUserRepository(t)
		mockRoleRepo := mocks.NewMockRoleRepository(t)
		mockAudit := mocks.NewMockAuditService(t)

		id := int64(999)
		mockStaffRepo.EXPECT().FindByID(mock.Anything, id).Return(sqlc.StaffUser{}, errors.New("not found")).Once()

		service := NewStaffService(mockStaffRepo, mockRoleRepo, mockAudit)
		staff, err := service.UpdateStatus(context.Background(), id, false)

		assert.Nil(t, staff)
		assert.ErrorIs(t, err, ErrStaffNotFound)
	})
}

// ============================================
// UPDATE PASSWORD
// ============================================

func TestStaffService_UpdatePassword(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		id            int64
		newPassword   string
		mockSetup     func(*mocks.MockStaffUserRepository, *mocks.MockAuditService)
		expectedError error
	}

	tests := []testCase{
		{
			name:        "success",
			id:          1,
			newPassword: "newpass",
			mockSetup: func(repo *mocks.MockStaffUserRepository, audit *mocks.MockAuditService) {
				repo.EXPECT().FindByID(mock.Anything, int64(1)).Return(dummyStaffUser(1), nil).Once()
				repo.EXPECT().UpdatePassword(mock.Anything, mock.MatchedBy(func(p sqlc.UpdateStaffPasswordParams) bool {
					return p.ID == int64(1) && p.PasswordHash != ""
				})).Return(nil).Once()
				audit.EXPECT().Log(mock.Anything, mock.Anything).Return(nil).Once()
			},
			expectedError: nil,
		},
		{
			name:        "staff not found",
			id:          999,
			newPassword: "newpass",
			mockSetup: func(repo *mocks.MockStaffUserRepository, audit *mocks.MockAuditService) {
				repo.EXPECT().FindByID(mock.Anything, int64(999)).Return(sqlc.StaffUser{}, errors.New("not found")).Once()
			},
			expectedError: ErrStaffNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockStaffRepo := mocks.NewMockStaffUserRepository(t)
			mockRoleRepo := mocks.NewMockRoleRepository(t)
			mockAudit := mocks.NewMockAuditService(t)

			tc.mockSetup(mockStaffRepo, mockAudit)

			service := NewStaffService(mockStaffRepo, mockRoleRepo, mockAudit)
			err := service.UpdatePassword(context.Background(), tc.id, tc.newPassword)

			assert.ErrorIs(t, err, tc.expectedError)
		})
	}
}

// ============================================
// DELETE
// ============================================

func TestStaffService_Delete(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		targetID      int64
		ctx           context.Context
		mockSetup     func(*mocks.MockStaffUserRepository, *mocks.MockAuditService)
		expectedError error
	}

	tests := []testCase{
		{
			name:     "success – actor different from target",
			targetID: 1,
			ctx:      ctxWithStaffID(2),
			mockSetup: func(repo *mocks.MockStaffUserRepository, audit *mocks.MockAuditService) {
				repo.EXPECT().FindByID(mock.Anything, int64(1)).Return(dummyStaffUser(1), nil).Once()
				repo.EXPECT().Delete(mock.Anything, int64(1)).Return(nil).Once()
				audit.EXPECT().Log(mock.Anything, mock.Anything).Return(nil).Once()
			},
			expectedError: nil,
		},
		{
			name:     "staff not found",
			targetID: 999,
			ctx:      ctxWithStaffID(2),
			mockSetup: func(repo *mocks.MockStaffUserRepository, audit *mocks.MockAuditService) {
				repo.EXPECT().FindByID(mock.Anything, int64(999)).Return(sqlc.StaffUser{}, errors.New("not found")).Once()
			},
			expectedError: ErrStaffNotFound,
		},
		{
			name:     "self deletion – actor equals target",
			targetID: 1,
			ctx:      ctxWithStaffID(1),
			mockSetup: func(repo *mocks.MockStaffUserRepository, audit *mocks.MockAuditService) {
				repo.EXPECT().FindByID(mock.Anything, int64(1)).Return(dummyStaffUser(1), nil).Once()
			},
			expectedError: ErrCannotDeleteSelf,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockStaffRepo := mocks.NewMockStaffUserRepository(t)
			mockRoleRepo := mocks.NewMockRoleRepository(t)
			mockAudit := mocks.NewMockAuditService(t)

			tc.mockSetup(mockStaffRepo, mockAudit)

			service := NewStaffService(mockStaffRepo, mockRoleRepo, mockAudit)
			err := service.Delete(tc.ctx, tc.targetID)

			assert.ErrorIs(t, err, tc.expectedError)
		})
	}
}
