package bootstrap

import (
	"context"
	"errors"

	"hello/internal/config"
	"hello/internal/database/repository"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type Bootstrap struct {
	roleRepo  *repository.RoleRepository
	staffRepo *repository.StaffRepository
	cfg       *config.Config
}

func New(
	roleRepo *repository.RoleRepository,
	staffRepo *repository.StaffRepository,
	cfg *config.Config,
) *Bootstrap {
	return &Bootstrap{
		roleRepo:  roleRepo,
		staffRepo: staffRepo,
		cfg:       cfg,
	}
}

func (b *Bootstrap) Run(ctx context.Context) error {
	_, err := b.staffRepo.FindByEmail(
		ctx,
		b.cfg.BootstrapAdminEmail,
	)

	if err == nil {
		return nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	role, err := b.roleRepo.FindByName(
		ctx,
		"SUPER_ADMIN",
	)
	if err != nil {
		return err
	}

	hash, err := bcrypt.GenerateFromPassword(
		[]byte(b.cfg.BootstrapAdminPassword),
		bcrypt.DefaultCost,
	)
	if err != nil {
		return err
	}

	_, err = b.staffRepo.Create(
		ctx,
		role.ID,
		b.cfg.BootstrapAdminName,
		b.cfg.BootstrapAdminEmail,
		string(hash),
	)

	return err
}
