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
	CreateSession(ctx context.Context, token *model.RefreshToken) error
	Rotate(ctx context.Context, tokenHash string, now time.Time, replacement *model.RefreshToken) (uuid.UUID, error)
	Revoke(ctx context.Context, tokenHash string, now time.Time) error
	IsActiveSession(ctx context.Context, sessionID, userID uuid.UUID, now time.Time) (bool, error)
}

type GormRefreshTokenRepository struct {
	db *gorm.DB
}

func NewRefreshTokenRepository(db *gorm.DB) *GormRefreshTokenRepository {
	return &GormRefreshTokenRepository{db: db}
}

func (r *GormRefreshTokenRepository) CreateSession(ctx context.Context, token *model.RefreshToken) error {
	if token.ID == uuid.Nil {
		token.ID = uuid.New()
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id").First(&user, "id = ?", token.UserID).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := tx.Model(&model.RefreshToken{}).
			Where("user_id = ? AND revoked_at IS NULL", token.UserID).
			Update("revoked_at", now).Error; err != nil {
			return err
		}
		return tx.Create(token).Error
	})
}

func (r *GormRefreshTokenRepository) Rotate(ctx context.Context, tokenHash string, now time.Time, replacement *model.RefreshToken) (uuid.UUID, error) {
	var candidate model.RefreshToken
	if err := r.db.WithContext(ctx).Select("user_id").Where("token_hash = ?", tokenHash).First(&candidate).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return uuid.Nil, ErrInvalidRefreshToken
		}
		return uuid.Nil, err
	}

	var userID uuid.UUID
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id").First(&user, "id = ?", candidate.UserID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInvalidRefreshToken
			}
			return err
		}

		var current model.RefreshToken
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token_hash = ? AND user_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", tokenHash, user.ID, now).
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

func (r *GormRefreshTokenRepository) IsActiveSession(ctx context.Context, sessionID, userID uuid.UUID, now time.Time) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.RefreshToken{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", sessionID, userID, now).
		Count(&count).Error
	return count > 0, err
}
