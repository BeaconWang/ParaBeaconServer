package repository

import (
	"context"
	"errors"

	"github.com/parabeacon/server/internal/model"
	"gorm.io/gorm"
)

var (
	ErrAppReleaseNotFound      = errors.New("app release not found")
	ErrAppReleaseAlreadyExists = errors.New("app release version already exists")
)

type AppReleaseRepository interface {
	Create(ctx context.Context, release *model.AppRelease) error
	Latest(ctx context.Context, packageName string) (*model.AppRelease, error)
	FindPublished(ctx context.Context, packageName string, versionCode int64) (*model.AppRelease, error)
}

type GormAppReleaseRepository struct {
	db *gorm.DB
}

func NewAppReleaseRepository(db *gorm.DB) *GormAppReleaseRepository {
	return &GormAppReleaseRepository{db: db}
}

func (r *GormAppReleaseRepository) Create(ctx context.Context, release *model.AppRelease) error {
	err := r.db.WithContext(ctx).Create(release).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrAppReleaseAlreadyExists
	}
	return err
}

func (r *GormAppReleaseRepository) Latest(ctx context.Context, packageName string) (*model.AppRelease, error) {
	var release model.AppRelease
	err := r.db.WithContext(ctx).
		Where("package_name = ? AND published_at IS NOT NULL", packageName).
		Order("version_code DESC").
		First(&release).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAppReleaseNotFound
	}
	if err != nil {
		return nil, err
	}
	return &release, nil
}

func (r *GormAppReleaseRepository) FindPublished(ctx context.Context, packageName string, versionCode int64) (*model.AppRelease, error) {
	var release model.AppRelease
	err := r.db.WithContext(ctx).
		Where("package_name = ? AND version_code = ? AND published_at IS NOT NULL", packageName, versionCode).
		First(&release).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAppReleaseNotFound
	}
	if err != nil {
		return nil, err
	}
	return &release, nil
}
