package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SessionMetricRepository interface {
	Ingest(ctx context.Context, event *model.SessionMetricEvent) error
}

type InstalledClientRepository interface {
	ListInstalledClients(ctx context.Context, limit int) ([]model.InstalledClient, error)
	GetInstalledClient(ctx context.Context, hardwareID uuid.UUID, eventLimit int) (*model.InstalledClientDetail, error)
}

type GormSessionMetricRepository struct {
	db *gorm.DB
}

func NewSessionMetricRepository(db *gorm.DB) *GormSessionMetricRepository {
	return &GormSessionMetricRepository{db: db}
}

func (r *GormSessionMetricRepository) ListInstalledClients(ctx context.Context, limit int) ([]model.InstalledClient, error) {
	if limit < 1 {
		limit = 100
	}
	var clients []model.InstalledClient
	err := r.db.WithContext(ctx).
		Table("session_metric_events AS e").
		Select(`
			e.hardware_id,
			e.user_id,
			COALESCE(u.username, '') AS username,
			e.app_version,
			e.client_ip,
			e.client_timestamp,
			e.server_received_at,
			(SELECT COUNT(DISTINCT sessions.session_id) FROM session_metric_events AS sessions WHERE sessions.hardware_id = e.hardware_id) AS session_count,
			(SELECT COUNT(*) FROM session_metric_events AS events WHERE events.hardware_id = e.hardware_id) AS event_count,
			COALESCE(runtime.total_runtime_sec, 0) AS total_runtime_sec,
			e.diagnostics AS latest_diagnostics`).
		Joins("LEFT JOIN users AS u ON u.id = e.user_id").
		Joins("LEFT JOIN session_metric_device_runtimes AS runtime ON runtime.hardware_id = e.hardware_id").
		Where(`NOT EXISTS (
			SELECT 1 FROM session_metric_events AS newer
			WHERE newer.hardware_id = e.hardware_id
			AND (newer.server_received_at > e.server_received_at OR
				(newer.server_received_at = e.server_received_at AND newer.id > e.id))
		)`).
		Order("e.server_received_at DESC").
		Limit(limit).
		Scan(&clients).Error
	return clients, err
}

func (r *GormSessionMetricRepository) GetInstalledClient(ctx context.Context, hardwareID uuid.UUID, eventLimit int) (*model.InstalledClientDetail, error) {
	if eventLimit < 1 {
		eventLimit = 100
	}
	var client model.InstalledClient
	err := r.db.WithContext(ctx).
		Table("session_metric_events AS e").
		Select(`
			e.hardware_id,
			e.user_id,
			COALESCE(u.username, '') AS username,
			e.app_version,
			e.client_ip,
			e.client_timestamp,
			e.server_received_at,
			(SELECT COUNT(DISTINCT sessions.session_id) FROM session_metric_events AS sessions WHERE sessions.hardware_id = e.hardware_id) AS session_count,
			(SELECT COUNT(*) FROM session_metric_events AS events WHERE events.hardware_id = e.hardware_id) AS event_count,
			COALESCE(runtime.total_runtime_sec, 0) AS total_runtime_sec,
			e.diagnostics AS latest_diagnostics`).
		Joins("LEFT JOIN users AS u ON u.id = e.user_id").
		Joins("LEFT JOIN session_metric_device_runtimes AS runtime ON runtime.hardware_id = e.hardware_id").
		Where("e.hardware_id = ?", hardwareID).
		Where(`NOT EXISTS (
			SELECT 1 FROM session_metric_events AS newer
			WHERE newer.hardware_id = e.hardware_id
			AND (newer.server_received_at > e.server_received_at OR
				(newer.server_received_at = e.server_received_at AND newer.id > e.id))
		)`).
		Take(&client).Error
	if err != nil {
		return nil, err
	}

	var events []model.InstalledClientEvent
	if err := r.db.WithContext(ctx).
		Table("session_metric_events").
		Select("event_id, session_id, user_id, app_version, client_ip, run_time_sec, total_runtime_sec, client_timestamp, server_received_at, diagnostics").
		Where("hardware_id = ?", hardwareID).
		Order("server_received_at DESC, id DESC").
		Limit(eventLimit).
		Find(&events).Error; err != nil {
		return nil, err
	}
	return &model.InstalledClientDetail{InstalledClient: client, Events: events}, nil
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
