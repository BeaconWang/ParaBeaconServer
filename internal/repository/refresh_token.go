package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidRefreshToken = errors.New("invalid refresh token")

type RefreshTokenRepository interface {
	Create(ctx context.Context, token *model.RefreshToken) error
	Rotate(ctx context.Context, tokenHash string, now time.Time, replacement *model.RefreshToken) (uuid.UUID, error)
	Revoke(ctx context.Context, tokenHash string, now time.Time) error
}

type GormRefreshTokenRepository struct {
	db *gorm.DB
}

func NewRefreshTokenRepository(db *gorm.DB) *GormRefreshTokenRepository {
	return &GormRefreshTokenRepository{db: db}
}

func (r *GormRefreshTokenRepository) Create(ctx context.Context, token *model.RefreshToken) error {
	if token.ID == uuid.Nil {
		token.ID = uuid.New()
	}
	return r.db.WithContext(ctx).Create(token).Error
}

func (r *GormRefreshTokenRepository) Rotate(ctx context.Context, tokenHash string, now time.Time, replacement *model.RefreshToken) (uuid.UUID, error) {
	var userID uuid.UUID
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current model.RefreshToken
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token_hash = ? AND revoked_at IS NULL AND expires_at > ?", tokenHash, now).
			First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInvalidRefreshToken
			}
			return err
		}
		if replacement.ID == uuid.Nil {
			replacement.ID = uuid.New()
		}
		replacement.UserID = current.UserID
		revokedAt := now
		if err := tx.Model(&current).Update("revoked_at", revokedAt).Error; err != nil {
			return err
		}
		if err := tx.Create(replacement).Error; err != nil {
			return err
		}
		userID = current.UserID
		return nil
	})
	return userID, err
}

func (r *GormRefreshTokenRepository) Revoke(ctx context.Context, tokenHash string, now time.Time) error {
	return r.db.WithContext(ctx).
		Model(&model.RefreshToken{}).
		Where("token_hash = ? AND revoked_at IS NULL", tokenHash).
		Update("revoked_at", now).Error
}
