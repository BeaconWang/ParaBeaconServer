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

var (
	ErrFlightLogSyncNotFound = errors.New("flight log sync not found or expired")
	ErrFlightLogSyncConflict = errors.New("flight log sync conflict")
)

type FlightLogRepository interface {
	Import(ctx context.Context, userID uuid.UUID, logs []model.FlightLog) (int, int, error)
	Replace(ctx context.Context, userID uuid.UUID, logs []model.FlightLog) error
	BeginSync(ctx context.Context, syncSession *model.FlightLogSync) error
	StageSyncBatch(ctx context.Context, userID, syncID uuid.UUID, batchIndex int, payloadHash string, logs []model.FlightLog, batchBytes int64, now time.Time) error
	CommitSync(ctx context.Context, userID, syncID uuid.UUID, now time.Time) (int, error)
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
		return replaceFlightLogsTx(tx, userID, logs)
	})
}

func replaceFlightLogsTx(tx *gorm.DB, userID uuid.UUID, logs []model.FlightLog) error {
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
}

func (r *GormFlightLogRepository) BeginSync(ctx context.Context, syncSession *model.FlightLogSync) error {
	now := time.Now().UTC()
	if err := r.deleteExpiredSyncs(ctx, now); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockFlightLogOwner(tx, syncSession.UserID); err != nil {
			return err
		}
		return tx.Create(syncSession).Error
	})
}

func (r *GormFlightLogRepository) deleteExpiredSyncs(ctx context.Context, now time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		expiredIDs := tx.Model(&model.FlightLogSync{}).Select("id").Where("expires_at <= ?", now)
		if err := tx.Where("sync_id IN (?)", expiredIDs).Delete(&model.FlightLogSyncRecord{}).Error; err != nil {
			return err
		}
		expiredIDs = tx.Model(&model.FlightLogSync{}).Select("id").Where("expires_at <= ?", now)
		if err := tx.Where("sync_id IN (?)", expiredIDs).Delete(&model.FlightLogSyncBatch{}).Error; err != nil {
			return err
		}
		return tx.Where("expires_at <= ?", now).Delete(&model.FlightLogSync{}).Error
	})
}

func (r *GormFlightLogRepository) StageSyncBatch(ctx context.Context, userID, syncID uuid.UUID, batchIndex int, payloadHash string, logs []model.FlightLog, batchBytes int64, now time.Time) error {
	if batchIndex < 0 || len(logs) == 0 || batchBytes < 0 || batchBytes > 18<<20 {
		return ErrFlightLogSyncConflict
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockFlightLogOwner(tx, userID); err != nil {
			return err
		}
		var syncSession model.FlightLogSync
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ? AND expires_at > ?", syncID, userID, now).
			First(&syncSession).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrFlightLogSyncNotFound
			}
			return err
		}
		if syncSession.CommittedAt != nil {
			return ErrFlightLogSyncConflict
		}

		var previous model.FlightLogSyncBatch
		err := tx.Where("sync_id = ? AND user_id = ? AND batch_index = ?", syncID, userID, batchIndex).First(&previous).Error
		if err == nil {
			if previous.PayloadHash == payloadHash && previous.RecordCount == len(logs) {
				return nil
			}
			return ErrFlightLogSyncConflict
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if batchIndex != syncSession.NextBatchIndex || syncSession.StagedCount+len(logs) > syncSession.ExpectedCount ||
			syncSession.StagedBytes+batchBytes > 512<<20 {
			return ErrFlightLogSyncConflict
		}

		batch := model.FlightLogSyncBatch{
			SyncID:      syncID,
			UserID:      userID,
			BatchIndex:  batchIndex,
			PayloadHash: payloadHash,
			RecordCount: len(logs),
		}
		if err := tx.Create(&batch).Error; err != nil {
			return err
		}
		records := make([]model.FlightLogSyncRecord, len(logs))
		for index, log := range logs {
			records[index] = model.FlightLogSyncRecord{
				StagedID:    uuid.New(),
				SyncID:      syncID,
				UserID:      userID,
				BatchIndex:  batchIndex,
				RecordIndex: index,
				StartedAt:   log.StartedAt,
				PointCount:  log.PointCount,
				Data:        log.Data,
			}
		}
		if err := tx.CreateInBatches(&records, 100).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return ErrFlightLogSyncConflict
			}
			return err
		}
		syncSession.StagedCount += len(records)
		syncSession.StagedBytes += batchBytes
		syncSession.NextBatchIndex++
		return tx.Model(&syncSession).Updates(map[string]any{
			"staged_count":     syncSession.StagedCount,
			"staged_bytes":     syncSession.StagedBytes,
			"next_batch_index": syncSession.NextBatchIndex,
		}).Error
	})
}

func (r *GormFlightLogRepository) CommitSync(ctx context.Context, userID, syncID uuid.UUID, now time.Time) (int, error) {
	var synced int
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockFlightLogOwner(tx, userID); err != nil {
			return err
		}
		var syncSession model.FlightLogSync
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ? AND expires_at > ?", syncID, userID, now).
			First(&syncSession).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrFlightLogSyncNotFound
			}
			return err
		}
		if syncSession.CommittedAt != nil {
			synced = syncSession.ExpectedCount
			return nil
		}
		var batchCount, recordCount int64
		if err := tx.Model(&model.FlightLogSyncBatch{}).Where("sync_id = ? AND user_id = ?", syncID, userID).Count(&batchCount).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.FlightLogSyncRecord{}).Where("sync_id = ? AND user_id = ?", syncID, userID).Count(&recordCount).Error; err != nil {
			return err
		}
		if syncSession.StagedCount != syncSession.ExpectedCount || int(recordCount) != syncSession.ExpectedCount || int(batchCount) != syncSession.NextBatchIndex {
			return ErrFlightLogSyncConflict
		}

		if err := replaceFlightLogsFromSyncTx(tx, userID, syncID); err != nil {
			return err
		}
		if err := tx.Where("sync_id = ? AND user_id = ?", syncID, userID).Delete(&model.FlightLogSyncRecord{}).Error; err != nil {
			return err
		}
		if err := tx.Where("sync_id = ? AND user_id = ?", syncID, userID).Delete(&model.FlightLogSyncBatch{}).Error; err != nil {
			return err
		}
		committedAt := now
		if err := tx.Model(&syncSession).Updates(map[string]any{
			"committed_at": committedAt,
			"staged_count": 0,
			"staged_bytes": 0,
		}).Error; err != nil {
			return err
		}
		synced = syncSession.ExpectedCount
		return nil
	})
	return synced, err
}

func replaceFlightLogsFromSyncTx(tx *gorm.DB, userID, syncID uuid.UUID) error {
	if err := tx.Exec(`
		DELETE FROM flight_logs AS current_logs
		WHERE current_logs.user_id = ?
		  AND NOT EXISTS (
			SELECT 1
			FROM flight_log_sync_records AS staged
			WHERE staged.sync_id = ?
			  AND staged.user_id = ?
			  AND staged.started_at = current_logs.started_at
			  AND staged.point_count = current_logs.point_count
		  )
	`, userID, syncID, userID).Error; err != nil {
		return err
	}
	return tx.Exec(`
		INSERT INTO flight_logs (id, user_id, started_at, point_count, data, created_at)
		SELECT staged.staged_id, staged.user_id, staged.started_at, staged.point_count, staged.data, CURRENT_TIMESTAMP
		FROM flight_log_sync_records AS staged
		WHERE staged.sync_id = ? AND staged.user_id = ?
		ON CONFLICT (user_id, started_at, point_count)
		DO UPDATE SET data = EXCLUDED.data
	`, syncID, userID).Error
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
