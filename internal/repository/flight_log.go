package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type FlightLogRepository interface {
	Import(ctx context.Context, userID uuid.UUID, logs []model.FlightLog) (int, int, error)
	List(ctx context.Context, userID, after uuid.UUID, limit int) ([]model.FlightLog, bool, error)
}

type GormFlightLogRepository struct {
	db *gorm.DB
}

func NewFlightLogRepository(db *gorm.DB) *GormFlightLogRepository {
	return &GormFlightLogRepository{db: db}
}

func (r *GormFlightLogRepository) Import(ctx context.Context, userID uuid.UUID, logs []model.FlightLog) (accepted, skipped int, err error) {
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, log := range logs {
			var count int64
			if err := tx.Model(&model.FlightLog{}).
				Where("user_id = ? AND point_count = ? AND started_at BETWEEN ? AND ?", userID, log.PointCount, log.StartedAt.Add(-5*time.Second), log.StartedAt.Add(5*time.Second)).
				Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				skipped++
				continue
			}
			log.ID = uuid.New()
			log.UserID = userID
			result := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "user_id"}, {Name: "started_at"}, {Name: "point_count"}},
				DoNothing: true,
			}).Create(&log)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				skipped++
			} else {
				accepted++
			}
		}
		return nil
	})
	return accepted, skipped, err
}

func (r *GormFlightLogRepository) List(ctx context.Context, userID, after uuid.UUID, limit int) ([]model.FlightLog, bool, error) {
	logs := make([]model.FlightLog, 0, limit+1)
	query := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if after != uuid.Nil {
		query = query.Where("id < ?", after)
	}
	if err := query.Order("id DESC").Limit(limit + 1).Find(&logs).Error; err != nil {
		return nil, false, err
	}
	hasMore := len(logs) > limit
	if hasMore {
		logs = logs[:limit]
	}
	return logs, hasMore, nil
}
