package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"gorm.io/gorm"
)

type UserRepository interface {
	Create(ctx context.Context, user *model.User) error
	FindByLogin(ctx context.Context, identifier string) (*model.User, error)
	FindByID(ctx context.Context, id uuid.UUID) (*model.User, error)
	UpdateDisplayName(ctx context.Context, id uuid.UUID, displayName string) error
}

type GormUserRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) *GormUserRepository {
	return &GormUserRepository{db: db}
}

func (r *GormUserRepository) Create(ctx context.Context, user *model.User) error {
	if user.PilotNumber == nil {
		var pilotNumber int
		if err := r.db.WithContext(ctx).Raw("SELECT nextval('pilot_number_seq')").Scan(&pilotNumber).Error; err != nil {
			return fmt.Errorf("allocate pilot number: %w", err)
		}
		user.PilotNumber = &pilotNumber
	}
	if err := r.db.WithContext(ctx).Create(user).Error; err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

func (r *GormUserRepository) FindByLogin(ctx context.Context, identifier string) (*model.User, error) {
	var user model.User
	if err := r.db.WithContext(ctx).Where("email = ? OR username = ?", identifier, identifier).First(&user).Error; err != nil {
		return nil, fmt.Errorf("find user by login identifier: %w", err)
	}
	return &user, nil
}

func (r *GormUserRepository) FindByID(ctx context.Context, id uuid.UUID) (*model.User, error) {
	var user model.User
	if err := r.db.WithContext(ctx).First(&user, "id = ?", id).Error; err != nil {
		return nil, fmt.Errorf("find user by id: %w", err)
	}
	return &user, nil
}

func (r *GormUserRepository) UpdateDisplayName(ctx context.Context, id uuid.UUID, displayName string) error {
	result := r.db.WithContext(ctx).Model(&model.User{}).Where("id = ?", id).Update("display_name", displayName)
	if result.Error != nil {
		return fmt.Errorf("update user display name: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
