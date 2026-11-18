package model

import (
	"time"

	"github.com/google/uuid"
)

type FlightLogSync struct {
	ID             uuid.UUID  `gorm:"type:uuid;primaryKey"`
	UserID         uuid.UUID  `gorm:"type:uuid;not null;index"`
	ExpectedCount  int        `gorm:"not null"`
	StagedCount    int        `gorm:"not null;default:0"`
	NextBatchIndex int        `gorm:"not null;default:0"`
	ArchiveMode    bool       `gorm:"not null;default:false"`
	StagedBytes    int64      `gorm:"not null;default:0"`
	ExpiresAt      time.Time  `gorm:"not null;index"`
	CommittedAt    *time.Time `gorm:"index"`
	CreatedAt      time.Time
}

type FlightLogSyncBatch struct {
	SyncID      uuid.UUID `gorm:"type:uuid;primaryKey;index:idx_flight_log_sync_batch_owner,priority:1"`
	UserID      uuid.UUID `gorm:"type:uuid;not null;index:idx_flight_log_sync_batch_owner,priority:2"`
	BatchIndex  int       `gorm:"primaryKey"`
	PayloadHash string    `gorm:"size:64;not null"`
	RecordCount int       `gorm:"not null"`
	CreatedAt   time.Time
}

type FlightLogSyncRecord struct {
	StagedID    uuid.UUID  `gorm:"column:staged_id;type:uuid;not null;uniqueIndex"`
	SyncID      uuid.UUID  `gorm:"type:uuid;primaryKey;uniqueIndex:idx_flight_log_sync_record_identity,priority:1"`
	UserID      uuid.UUID  `gorm:"type:uuid;not null;index"`
	BatchIndex  int        `gorm:"primaryKey"`
	RecordIndex int        `gorm:"primaryKey"`
	StartedAt   time.Time  `gorm:"not null;uniqueIndex:idx_flight_log_sync_record_identity,priority:2"`
	PointCount  int        `gorm:"not null;uniqueIndex:idx_flight_log_sync_record_identity,priority:3"`
	ArchiveID   *uuid.UUID `gorm:"type:uuid"`
	Data        string     `gorm:"type:jsonb;not null"`
	CreatedAt   time.Time
}
