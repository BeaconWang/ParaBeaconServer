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
	ListPublished(ctx context.Context, packageName string) ([]model.AppRelease, error)
	Latest(ctx context.Context, packageName string) (*model.AppRelease, error)
	FindPublished(ctx context.Context, packageName string, versionCode int64) (*model.AppRelease, error)
	IncrementDownloadCount(ctx context.Context, packageName string, versionCode int64) error
	Delete(ctx context.Context, packageName string, versionCode int64) error
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

func (r *GormAppReleaseRepository) ListPublished(ctx context.Context, packageName string) ([]model.AppRelease, error) {
	var releases []model.AppRelease
	if err := r.db.WithContext(ctx).
		Where("package_name = ? AND published_at IS NOT NULL", packageName).
		Order("version_code DESC").
		Limit(100).
		Find(&releases).Error; err != nil {
		return nil, err
	}
	return releases, nil
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

func (r *GormAppReleaseRepository) IncrementDownloadCount(ctx context.Context, packageName string, versionCode int64) error {
	result := r.db.WithContext(ctx).
		Model(&model.AppRelease{}).
		Where("package_name = ? AND version_code = ? AND published_at IS NOT NULL", packageName, versionCode).
		UpdateColumn("download_count", gorm.Expr("download_count + ?", 1))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAppReleaseNotFound
	}
	return nil
}

func (r *GormAppReleaseRepository) Delete(ctx context.Context, packageName string, versionCode int64) error {
	result := r.db.WithContext(ctx).
		Where("package_name = ? AND version_code = ? AND published_at IS NOT NULL", packageName, versionCode).
		Delete(&model.AppRelease{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAppReleaseNotFound
	}
	return nil
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
