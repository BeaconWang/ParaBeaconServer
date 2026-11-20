package repository

import (
	"context"

	"github.com/parabeacon/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SessionMetricRepository interface {
	Ingest(ctx context.Context, event *model.SessionMetricEvent) error
}

type GormSessionMetricRepository struct {
	db *gorm.DB
}

func NewSessionMetricRepository(db *gorm.DB) *GormSessionMetricRepository {
	return &GormSessionMetricRepository{db: db}
}

func (r *GormSessionMetricRepository) Ingest(ctx context.Context, event *model.SessionMetricEvent) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "hardware_id"}, {Name: "session_id"}, {Name: "event_id"}},
			DoNothing: true,
		}).Create(event)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}

		runtime := model.SessionMetricRuntime{
			HardwareID: event.HardwareID,
			SessionID:  event.SessionID,
			RunTimeSec: 0,
			UpdatedAt:  event.ServerReceivedAt,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "hardware_id"}, {Name: "session_id"}},
			DoNothing: true,
		}).Create(&runtime).Error; err != nil {
			return err
		}

		var current model.SessionMetricRuntime
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("hardware_id = ? AND session_id = ?", event.HardwareID, event.SessionID).
			Take(&current).Error; err != nil {
			return err
		}

		delta := int64(0)
		if event.RunTimeSec > current.RunTimeSec {
			delta = event.RunTimeSec - current.RunTimeSec
			if err := tx.Model(&model.SessionMetricRuntime{}).
				Where("hardware_id = ? AND session_id = ?", event.HardwareID, event.SessionID).
				Updates(map[string]interface{}{
					"run_time_sec": event.RunTimeSec,
					"updated_at":   event.ServerReceivedAt,
				}).Error; err != nil {
				return err
			}
		}

		deviceRuntime := model.SessionMetricDeviceRuntime{
			HardwareID:      event.HardwareID,
			TotalRuntimeSec: delta,
			UpdatedAt:       event.ServerReceivedAt,
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "hardware_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"total_runtime_sec": gorm.Expr("session_metric_device_runtimes.total_runtime_sec + ?", delta),
				"updated_at":        event.ServerReceivedAt,
			}),
		}).Create(&deviceRuntime).Error
	})
}
