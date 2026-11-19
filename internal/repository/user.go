package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type UserRepository interface {
	Create(ctx context.Context, user *model.User) error
	FindByLogin(ctx context.Context, identifier string) (*model.User, error)
	FindByID(ctx context.Context, id uuid.UUID) (*model.User, error)
	UpdateDisplayName(ctx context.Context, id uuid.UUID, displayName string) error
	UpdateChannelNumber(ctx context.Context, id uuid.UUID, channelNumber int) error
}

type PasswordChangeRepository interface {
	ChangePassword(ctx context.Context, id uuid.UUID, expectedHash, newHash string, changedAt time.Time) error
}

var ErrPasswordChanged = errors.New("password changed concurrently")

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

func (r *GormUserRepository) ChangePassword(ctx context.Context, id uuid.UUID, expectedHash, newHash string, changedAt time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id, password_hash").First(&user, "id = ?", id).Error; err != nil {
			return err
		}
		if user.PasswordHash != expectedHash {
			return ErrPasswordChanged
		}
		if err := tx.Model(&model.User{}).
			Where("id = ? AND password_hash = ?", id, expectedHash).
			Update("password_hash", newHash).Error; err != nil {
			return fmt.Errorf("update user password: %w", err)
		}
		return tx.Model(&model.RefreshToken{}).
			Where("user_id = ? AND revoked_at IS NULL", id).
			Update("revoked_at", changedAt).Error
	})
}

func (r *GormUserRepository) UpdateChannelNumber(ctx context.Context, id uuid.UUID, channelNumber int) error {
	result := r.db.WithContext(ctx).Model(&model.User{}).Where("id = ?", id).Update("channel_number", channelNumber)
	if result.Error != nil {
		return fmt.Errorf("update user channel number: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
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
