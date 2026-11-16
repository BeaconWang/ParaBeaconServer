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
	Replace(ctx context.Context, userID uuid.UUID, logs []model.FlightLog) error
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
		if err := lockFlightLogOwner(tx, userID); err != nil {
			return err
		}
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

func (r *GormFlightLogRepository) Replace(ctx context.Context, userID uuid.UUID, logs []model.FlightLog) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockFlightLogOwner(tx, userID); err != nil {
			return err
		}

		var existing []model.FlightLog
		if err := tx.Model(&model.FlightLog{}).
			Select("id", "started_at", "point_count").
			Where("user_id = ?", userID).
			Find(&existing).Error; err != nil {
			return err
		}
		existingByIdentity := make(map[flightLogIdentity]model.FlightLog, len(existing))
		for _, log := range existing {
			existingByIdentity[flightLogIdentity{startedAt: log.StartedAt.UnixMicro(), pointCount: log.PointCount}] = log
		}

		retainedIDs := make([]uuid.UUID, 0, len(logs))
		for index := range logs {
			logs[index].UserID = userID
			identity := flightLogIdentity{startedAt: logs[index].StartedAt.UnixMicro(), pointCount: logs[index].PointCount}
			if current, found := existingByIdentity[identity]; found {
				logs[index].ID = current.ID
				retainedIDs = append(retainedIDs, current.ID)
			} else {
				logs[index].ID = uuid.New()
			}
		}

		staleQuery := tx.Where("user_id = ?", userID)
		if len(retainedIDs) > 0 {
			staleQuery = staleQuery.Where("id NOT IN ?", retainedIDs)
		}
		if err := staleQuery.Delete(&model.FlightLog{}).Error; err != nil {
			return err
		}
		if len(logs) == 0 {
			return nil
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "started_at"}, {Name: "point_count"}},
			DoUpdates: clause.AssignmentColumns([]string{"data"}),
		}).CreateInBatches(&logs, 100).Error
	})
}

type flightLogIdentity struct {
	startedAt  int64
	pointCount int
}

func lockFlightLogOwner(tx *gorm.DB, userID uuid.UUID) error {
	var user model.User
	return tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id").First(&user, "id = ?", userID).Error
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
