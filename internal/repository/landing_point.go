package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type LandingPointRepository interface {
	Merge(ctx context.Context, userID uuid.UUID, points []model.LandingPoint, defaultID *string) error
	List(ctx context.Context, userID uuid.UUID) ([]model.LandingPoint, *string, error)
}

type GormLandingPointRepository struct {
	db *gorm.DB
}

func NewLandingPointRepository(db *gorm.DB) *GormLandingPointRepository {
	return &GormLandingPointRepository{db: db}
}

func (r *GormLandingPointRepository) Merge(ctx context.Context, userID uuid.UUID, points []model.LandingPoint, defaultID *string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for index := range points {
			points[index].UserID = userID
		}
		if len(points) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "user_id"}, {Name: "client_id"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"name", "latitude", "longitude", "altitude_m", "created_at", "notes", "updated_at",
				}),
			}).CreateInBatches(&points, 100).Error; err != nil {
				return err
			}
		}
		settings := model.LandingPointSettings{UserID: userID, DefaultID: defaultID}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"default_id", "updated_at"}),
		}).Create(&settings).Error
	})
}

func (r *GormLandingPointRepository) List(ctx context.Context, userID uuid.UUID) ([]model.LandingPoint, *string, error) {
	points := make([]model.LandingPoint, 0)
	if err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("name ASC, client_id ASC").
		Limit(101).
		Find(&points).Error; err != nil {
		return nil, nil, err
	}
	var settings model.LandingPointSettings
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&settings).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, err
	}
	return points, settings.DefaultID, nil
}
