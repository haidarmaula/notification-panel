package applications

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/authz"
	"hello/internal/database/sqlc"
	"hello/pkg/response"
)

var (
	errInvalidUserID = errors.New("invalid user id")
	errUserNotFound  = errors.New("user not found")
)

type userReadPage struct {
	after int64
	limit int32
}

type readPage[T any] struct {
	Items       []T     `json:"items"`
	NextAfterID *string `json:"next_after_id"`
}

type scopedUserDTO struct {
	ID         string             `json:"id"`
	ExternalID string             `json:"external_id"`
	Name       pgtype.Text        `json:"name"`
	Email      pgtype.Text        `json:"email"`
	Status     string             `json:"status"`
	CreatedAt  pgtype.Timestamptz `json:"created_at"`
	UpdatedAt  pgtype.Timestamptz `json:"updated_at"`
}

type scopedDeviceDTO struct {
	ID         string             `json:"id"`
	Platform   string             `json:"platform"`
	IsActive   bool               `json:"is_active"`
	LastSeenAt pgtype.Timestamptz `json:"last_seen_at"`
	CreatedAt  pgtype.Timestamptz `json:"created_at"`
	UpdatedAt  pgtype.Timestamptz `json:"updated_at"`
}

func userDTO(row sqlc.GetScopedUserRow) scopedUserDTO {
	return scopedUserDTO{strconv.FormatInt(row.ID, 10), row.ExternalID, row.Name, row.Email, row.Status, row.CreatedAt, row.UpdatedAt}
}

func makeReadPage[R, T any](rows []R, limit int32, convert func(R) (T, string)) readPage[T] {
	hasMore := len(rows) > int(limit)
	if hasMore {
		rows = rows[:limit]
	}
	page := readPage[T]{Items: make([]T, 0, len(rows))}
	for i, row := range rows {
		item, id := convert(row)
		page.Items = append(page.Items, item)
		if hasMore && i == len(rows)-1 {
			page.NextAfterID = &id
		}
	}
	return page
}

func (s *Service) requireUserRead(ctx context.Context, id pgtype.UUID) (authz.ApplicationPrincipal, error) {
	return authz.NewApplicationAuthorizer(s.q).RequireApplication(ctx, id, authz.UsersRead)
}

func (s *Service) scopedUser(ctx context.Context, appID, userID int64) (sqlc.GetScopedUserRow, error) {
	if userID <= 0 {
		return sqlc.GetScopedUserRow{}, errInvalidUserID
	}
	row, err := s.q.GetScopedUser(ctx, sqlc.GetScopedUserParams{ApplicationID: appID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, errUserNotFound
	}
	return row, err
}

func (s *Service) getUser(ctx context.Context, id pgtype.UUID, userID int64) (sqlc.GetScopedUserRow, error) {
	scope, err := s.requireUserRead(ctx, id)
	if err != nil {
		return sqlc.GetScopedUserRow{}, err
	}
	return s.scopedUser(ctx, scope.ApplicationID, userID)
}

func (s *Service) listUsers(ctx context.Context, id pgtype.UUID, page userReadPage) ([]sqlc.ListScopedUsersRow, error) {
	scope, err := s.requireUserRead(ctx, id)
	if err != nil {
		return nil, err
	}
	if !page.valid() {
		return nil, ErrInvalidPagination
	}
	return s.q.ListScopedUsers(ctx, sqlc.ListScopedUsersParams{ApplicationID: scope.ApplicationID, AfterID: page.after, PageSize: page.limit + 1})
}

func (s *Service) listUserDevices(ctx context.Context, id pgtype.UUID, userID int64, page userReadPage) ([]sqlc.ListScopedUserDevicesRow, error) {
	scope, err := s.requireUserRead(ctx, id)
	if err != nil {
		return nil, err
	}
	if !page.valid() {
		return nil, ErrInvalidPagination
	}
	if _, err := s.scopedUser(ctx, scope.ApplicationID, userID); err != nil {
		return nil, err
	}
	return s.q.ListScopedUserDevices(ctx, sqlc.ListScopedUserDevicesParams{ApplicationID: scope.ApplicationID, UserID: userID, AfterID: page.after, PageSize: page.limit + 1})
}

func (p userReadPage) valid() bool {
	return p.after >= 0 && p.limit >= 1 && p.limit <= 100
}

func readPagination(r *http.Request) (userReadPage, error) {
	v, err := query(r, "after_id", "limit")
	if err != nil {
		return userReadPage{}, err
	}
	p := userReadPage{limit: 20}
	if v.Has("after_id") {
		value := v.Get("after_id")
		p.after, err = strconv.ParseInt(value, 10, 64)
		if err != nil || (value != "0" && !staffIDPattern.MatchString(value)) {
			return p, ErrInvalidPagination
		}
	}
	if v.Has("limit") {
		value := v.Get("limit")
		n, err := strconv.ParseInt(value, 10, 32)
		if err != nil || !staffIDPattern.MatchString(value) {
			return p, ErrInvalidPagination
		}
		p.limit = int32(n)
	}
	if !p.valid() {
		return p, ErrInvalidPagination
	}
	return p, nil
}

func readUserID(r *http.Request) (int64, error) {
	value := r.PathValue("user_id")
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || !staffIDPattern.MatchString(value) {
		return 0, errInvalidUserID
	}
	return id, nil
}

func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	page, err := readPagination(r)
	if err != nil {
		writeError(w, err)
		return
	}
	rows, err := h.service.listUsers(r.Context(), id, page)
	if err != nil {
		writeError(w, err)
		return
	}
	result := makeReadPage(rows, page.limit, func(row sqlc.ListScopedUsersRow) (scopedUserDTO, string) {
		item := userDTO(sqlc.GetScopedUserRow(row))
		return item, item.ID
	})
	response.JSON(w, http.StatusOK, result, "success")
}

func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	userID, err := readUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err := query(r); err != nil {
		writeError(w, err)
		return
	}
	row, err := h.service.getUser(r.Context(), id, userID)
	if err != nil {
		writeError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, userDTO(row), "success")
}

func (h *Handler) ListUserDevices(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	userID, err := readUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	page, err := readPagination(r)
	if err != nil {
		writeError(w, err)
		return
	}
	rows, err := h.service.listUserDevices(r.Context(), id, userID, page)
	if err != nil {
		writeError(w, err)
		return
	}
	result := makeReadPage(rows, page.limit, func(row sqlc.ListScopedUserDevicesRow) (scopedDeviceDTO, string) {
		id := strconv.FormatInt(row.ID, 10)
		return scopedDeviceDTO{id, row.Platform, row.IsActive, row.LastSeenAt, row.CreatedAt, row.UpdatedAt}, id
	})
	response.JSON(w, http.StatusOK, result, "success")
}
