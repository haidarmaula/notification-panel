package applications

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/audit"
	"hello/internal/authz"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
)

type Transactor interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// Service requires real query/transaction dependencies. Every operation rechecks
// current DB permission; writes build audit and authority from their own tx.
// There is no optional checker, activation method or provider/Kafka dependency.
type Service struct {
	q  sqlc.Querier
	db Transactor

	promotion scheduleCursor
}

func NewService(q sqlc.Querier, db Transactor) *Service {
	if q == nil || db == nil {
		panic("applications: queries and transactor are required")
	}
	return &Service{q: q, db: db}
}
func authority(q sqlc.Querier) *authz.Authorizer {
	return authz.New(repository.NewStaffUserRepository(q))
}
func (s *Service) require(ctx context.Context, p authz.Permission) (authz.Principal, error) {
	return authority(s.q).Require(ctx, p)
}

// ListGranted returns only the current principal's active app memberships.
func (s *Service) ListGranted(ctx context.Context, page, limit int32) (repository.ApplicationPage, error) {
	principal, err := s.require(ctx, authz.ApplicationsRead)
	if err != nil {
		return repository.ApplicationPage{}, err
	}
	offset, err := pageOffset(page, limit)
	if err != nil {
		return repository.ApplicationPage{}, err
	}
	return repository.NewApplicationRepository(s.q).ListGranted(ctx, principal.ID, offset, limit)
}

// GetGranted requires an explicit app grant, even for SUPER_ADMIN.
func (s *Service) GetGranted(ctx context.Context, id pgtype.UUID) (sqlc.Application, error) {
	if _, err := s.require(ctx, authz.ApplicationsRead); err != nil {
		return sqlc.Application{}, err
	}
	if err := validID(id); err != nil {
		return sqlc.Application{}, err
	}
	scope, err := authz.NewApplicationAuthorizer(s.q).RequireApplication(ctx, id, authz.ApplicationsRead)
	if err != nil {
		return sqlc.Application{}, err
	}
	app, err := repository.NewApplicationRepository(s.q).FindByPublicID(ctx, id)
	if err != nil {
		return sqlc.Application{}, appError(err)
	}
	if app.ID != scope.ApplicationID {
		return sqlc.Application{}, errors.New("application identity changed")
	}
	return app, nil
}

// ListManaged lists registry metadata under global management permission.
func (s *Service) ListManaged(ctx context.Context, key string, page, limit int32) (repository.ApplicationPage, error) {
	if _, err := s.require(ctx, authz.ApplicationsManage); err != nil {
		return repository.ApplicationPage{}, err
	}
	if key != "" {
		if err := validateKey(key); err != nil {
			return repository.ApplicationPage{}, err
		}
	}
	offset, err := pageOffset(page, limit)
	if err != nil {
		return repository.ApplicationPage{}, err
	}
	return repository.NewApplicationRepository(s.q).ListManaged(ctx, key, offset, limit)
}

// GetManaged reads control-plane metadata; it grants no business access.
func (s *Service) GetManaged(ctx context.Context, id pgtype.UUID) (sqlc.Application, error) {
	if _, err := s.require(ctx, authz.ApplicationsManage); err != nil {
		return sqlc.Application{}, err
	}
	if err := validID(id); err != nil {
		return sqlc.Application{}, err
	}
	app, err := repository.NewApplicationRepository(s.q).FindByPublicID(ctx, id)
	return app, appError(err)
}

// ListGrants lists active and revoked grants under global management authority.
func (s *Service) ListGrants(ctx context.Context, id pgtype.UUID, page, limit int32) (repository.ApplicationGrantPage, error) {
	if _, err := s.require(ctx, authz.ApplicationsManage); err != nil {
		return repository.ApplicationGrantPage{}, err
	}
	if err := validID(id); err != nil {
		return repository.ApplicationGrantPage{}, err
	}
	offset, err := pageOffset(page, limit)
	if err != nil {
		return repository.ApplicationGrantPage{}, err
	}
	repo := repository.NewApplicationRepository(s.q)
	app, err := repo.FindByPublicID(ctx, id)
	if err != nil {
		return repository.ApplicationGrantPage{}, appError(err)
	}
	return repo.ListGrants(ctx, app.ID, offset, limit)
}

// GetGrant reads one control-plane grant, including its current revision.
func (s *Service) GetGrant(ctx context.Context, id pgtype.UUID, staffID int64) (sqlc.ApplicationStaffGrant, error) {
	if _, err := s.require(ctx, authz.ApplicationsManage); err != nil {
		return sqlc.ApplicationStaffGrant{}, err
	}
	if err := validID(id); err != nil {
		return sqlc.ApplicationStaffGrant{}, err
	}
	if staffID <= 0 {
		return sqlc.ApplicationStaffGrant{}, ErrInvalidStaffID
	}
	repo := repository.NewApplicationRepository(s.q)
	app, err := repo.FindByPublicID(ctx, id)
	if err != nil {
		return sqlc.ApplicationStaffGrant{}, appError(err)
	}
	grant, err := repo.GetGrant(ctx, app.ID, staffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.ApplicationStaffGrant{}, ErrGrantNotFound
	}
	return grant, err
}

// Create commits a provisioning project and audit together, without a creator grant.
func (s *Service) Create(ctx context.Context, key, name string) (sqlc.Application, error) {
	if _, err := s.require(ctx, authz.ApplicationsManage); err != nil {
		return sqlc.Application{}, err
	}
	if err := validateKey(key); err != nil {
		return sqlc.Application{}, err
	}
	name, err := cleanName(name)
	if err != nil {
		return sqlc.Application{}, err
	}
	var result sqlc.Application
	err = s.writeAsStaff(ctx, func(ctx context.Context, q *sqlc.Queries, session staffWriteSession, epoch pgtype.UUID) error {
		if err := session.lockActor(ctx, q, epoch); err != nil {
			return err
		}
		if _, err := authority(q).Require(ctx, authz.ApplicationsManage); err != nil {
			return err
		}
		app, err := repository.NewApplicationRepository(q).Create(ctx, key, name)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "applications_key_key" {
				return ErrKeyExists
			}
			return err
		}
		if err := logChange(ctx, q, app, audit.ACTION_APPLICATION_CREATE, audit.ENTITY_TYPE_APPLICATION, app.ID, nil, appSnapshot(app)); err != nil {
			return err
		}
		result = app
		return nil
	})
	if err != nil {
		return sqlc.Application{}, err
	}
	return result, nil
}

// Rename requires a current strong ETag and audits only an actual name change.
func (s *Service) Rename(ctx context.Context, id pgtype.UUID, name string, c Preconditions) (sqlc.Application, error) {
	if _, err := s.require(ctx, authz.ApplicationsManage); err != nil {
		return sqlc.Application{}, err
	}
	if err := validID(id); err != nil {
		return sqlc.Application{}, err
	}
	name, err := cleanName(name)
	if err != nil {
		return sqlc.Application{}, err
	}
	if err := c.validate(false); err != nil {
		return sqlc.Application{}, err
	}
	var result sqlc.Application
	err = s.writeAsStaff(ctx, func(ctx context.Context, q *sqlc.Queries, session staffWriteSession, epoch pgtype.UUID) error {
		repo := repository.NewApplicationRepository(q)
		app, err := lockManagedProject(ctx, q, id, session, epoch, 0)
		if err != nil {
			return err
		}
		if c.IfMatch != ApplicationETag(app) {
			return ErrPreconditionFailed
		}
		if name == app.Name {
			result = app
			return nil
		}
		updated, err := repo.Rename(ctx, app.ID, app.Revision, name)
		if err != nil {
			return casError(err)
		}
		if err := logChange(ctx, q, updated, audit.ACTION_APPLICATION_UPDATE, audit.ENTITY_TYPE_APPLICATION, app.ID, appSnapshot(app), appSnapshot(updated)); err != nil {
			return err
		}
		result = updated
		return nil
	})
	if err != nil {
		return sqlc.Application{}, err
	}
	return result, nil
}

// EnableGrant conditionally creates/reactivates membership with atomic audit.
func (s *Service) EnableGrant(ctx context.Context, id pgtype.UUID, staffID int64, c Preconditions) (GrantResult, error) {
	if _, err := s.require(ctx, authz.ApplicationsManage); err != nil {
		return GrantResult{}, err
	}
	if err := validID(id); err != nil {
		return GrantResult{}, err
	}
	if staffID <= 0 {
		return GrantResult{}, ErrInvalidStaffID
	}
	if err := c.validate(true); err != nil {
		return GrantResult{}, err
	}
	var result GrantResult
	err := s.writeAsStaff(ctx, func(ctx context.Context, q *sqlc.Queries, session staffWriteSession, epoch pgtype.UUID) error {
		app, err := lockManagedProject(ctx, q, id, session, epoch, staffID)
		if err != nil {
			return err
		}
		repo := repository.NewApplicationRepository(q)
		target, err := q.GetStaffAuthority(ctx, staffID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrStaffNotFound
		}
		if err != nil {
			return err
		}
		if target.ID != staffID {
			return errors.New("inconsistent grant target")
		}
		old, err := repo.GetGrant(ctx, app.ID, staffID)
		absent := errors.Is(err, pgx.ErrNoRows)
		if err != nil && !absent {
			return err
		}
		if absent && c.IfNoneMatch != "*" || !absent && (c.IfNoneMatch != "" || c.IfMatch != GrantETag(app, old)) {
			return ErrPreconditionFailed
		}
		if !target.IsActive {
			return ErrStaffInactive
		}
		if !absent && old.IsActive {
			result = GrantResult{Grant: old}
			return nil
		}
		var updated sqlc.ApplicationStaffGrant
		var before any
		if absent {
			updated, err = repo.CreateGrant(ctx, app.ID, staffID)
			if err != nil {
				var pgErr *pgconn.PgError
				if errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == "application_staff_grants_staff_id_fkey" {
					return ErrStaffNotFound
				}
			}
		} else {
			before = grantSnapshot(app, old)
			updated, err = repo.SetGrantActive(ctx, app.ID, staffID, old.Revision, true)
			err = casError(err)
		}
		if err != nil {
			return err
		}
		if err := logChange(ctx, q, app, audit.ACTION_APPLICATION_GRANT_ENABLE, audit.ENTITY_TYPE_APPLICATION_GRANT, staffID, before, grantSnapshot(app, updated)); err != nil {
			return err
		}
		result = GrantResult{Grant: updated, Created: absent}
		return nil
	})
	if err != nil {
		return GrantResult{}, err
	}
	return result, nil
}

// RevokeGrant conditionally soft-revokes membership; it permits inactive targets.
func (s *Service) RevokeGrant(ctx context.Context, id pgtype.UUID, staffID int64, c Preconditions) error {
	if _, err := s.require(ctx, authz.ApplicationsManage); err != nil {
		return err
	}
	if err := validID(id); err != nil {
		return err
	}
	if staffID <= 0 {
		return ErrInvalidStaffID
	}
	if err := c.validate(false); err != nil {
		return err
	}
	return s.writeAsStaff(ctx, func(ctx context.Context, q *sqlc.Queries, session staffWriteSession, epoch pgtype.UUID) error {
		app, err := lockManagedProject(ctx, q, id, session, epoch, staffID)
		if err != nil {
			return err
		}
		repo := repository.NewApplicationRepository(q)
		old, err := repo.GetGrant(ctx, app.ID, staffID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrGrantNotFound
		}
		if err != nil {
			return err
		}
		if c.IfMatch != GrantETag(app, old) {
			return ErrPreconditionFailed
		}
		if !old.IsActive {
			return nil
		}
		updated, err := repo.SetGrantActive(ctx, app.ID, staffID, old.Revision, false)
		if err != nil {
			return casError(err)
		}
		return logChange(ctx, q, app, audit.ACTION_APPLICATION_GRANT_REVOKE, audit.ENTITY_TYPE_APPLICATION_GRANT, staffID, grantSnapshot(app, old), grantSnapshot(app, updated))
	})
}
func appError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return authz.ErrApplicationNotFound
	}
	return err
}

// Lock control parent before principal lookup; the precheck already denied callers
// without management permission before any target lookup. No cached grant is used.
func lockManagedProject(ctx context.Context, q *sqlc.Queries, id pgtype.UUID, session staffWriteSession, epoch pgtype.UUID, target int64) (sqlc.Application, error) {
	app, err := repository.NewApplicationRepository(q).LockByPublicID(ctx, id)
	if err != nil {
		return sqlc.Application{}, appError(err)
	}
	if target == 0 {
		err = session.lockActor(ctx, q, epoch)
	} else {
		// Staff administration uses the same ID order. Grant writes also
		// fence target deactivation and deletion through the grant change.
		_, err = q.LockStaffMutationUsers(ctx, sqlc.LockStaffMutationUsersParams{Actor: session.actorID, Target: target})
		if err == nil {
			err = session.lockSession(ctx, q, epoch)
		}
	}
	if err != nil {
		return sqlc.Application{}, err
	}
	if _, err := authority(q).Require(ctx, authz.ApplicationsManage); err != nil {
		return sqlc.Application{}, err
	}
	return app, nil
}
func (s *Service) write(ctx context.Context, fn func(context.Context, *sqlc.Queries) error) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := s.attempt(ctx, fn)
		if err == nil {
			return nil
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || (pgErr.Code != "40001" && pgErr.Code != "40P01") || attempt == 2 {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return errors.New("application transaction retry exhausted")
}
func (s *Service) attempt(ctx context.Context, fn func(context.Context, *sqlc.Queries) error) error {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin application transaction: %w", err)
	}
	if tx == nil {
		return errors.New("application transaction unavailable")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if err := fn(ctx, sqlc.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit application transaction: %w", err)
	}
	return nil
}
func appSnapshot(app sqlc.Application) any {
	return struct {
		ID        pgtype.UUID `json:"id"`
		Key       string      `json:"key"`
		Name      string      `json:"name"`
		Status    string      `json:"status"`
		Revision  string      `json:"revision"`
		CreatedAt time.Time   `json:"created_at"`
		UpdatedAt time.Time   `json:"updated_at"`
	}{app.PublicID, app.Key, app.Name, app.Status, strconv.FormatInt(app.Revision, 10), app.CreatedAt.Time.UTC(), app.UpdatedAt.Time.UTC()}
}
func grantSnapshot(app sqlc.Application, g sqlc.ApplicationStaffGrant) any {
	return struct {
		ApplicationID pgtype.UUID `json:"application_id"`
		StaffID       string      `json:"staff_id"`
		IsActive      bool        `json:"is_active"`
		Revision      string      `json:"revision"`
		CreatedAt     time.Time   `json:"created_at"`
		UpdatedAt     time.Time   `json:"updated_at"`
	}{app.PublicID, strconv.FormatInt(g.StaffID, 10), g.IsActive, strconv.FormatInt(g.Revision, 10), g.CreatedAt.Time.UTC(), g.UpdatedAt.Time.UTC()}
}
func logChange(ctx context.Context, q *sqlc.Queries, app sqlc.Application, action, entity string, id int64, before, after any) error {
	return audit.NewAuditService(repository.NewAuditLogRepository(q)).Log(ctx, audit.LogParams{ApplicationID: app.ID, Action: action, EntityType: entity, EntityID: id, EntityName: app.Name, Before: before, After: after})
}

func casError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPreconditionFailed
	}
	return err
}
