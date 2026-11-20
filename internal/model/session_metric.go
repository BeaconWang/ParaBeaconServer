package model

import (
	"time"

	"github.com/google/uuid"
)

type SessionMetricEvent struct {
	ID               uuid.UUID  `gorm:"type:uuid;primaryKey"`
	EventID          uuid.UUID  `gorm:"type:uuid;not null;uniqueIndex:idx_session_metric_event_identity,priority:3"`
	SessionID        uuid.UUID  `gorm:"type:uuid;not null;index;uniqueIndex:idx_session_metric_event_identity,priority:2"`
	HardwareID       uuid.UUID  `gorm:"type:uuid;not null;index;uniqueIndex:idx_session_metric_event_identity,priority:1;index:idx_session_metric_hardware_received,priority:1"`
	UserID           *uuid.UUID `gorm:"type:uuid;index;index:idx_session_metric_user_received,priority:1"`
	AppVersion       string     `gorm:"size:128;not null;index"`
	ClientIP         string     `gorm:"size:45;not null"`
	RunTimeSec       int64      `gorm:"not null;check:session_metric_event_runtime_nonnegative,run_time_sec >= 0"`
	TotalRuntimeSec  *int64     `gorm:"check:session_metric_event_total_runtime_nonnegative,total_runtime_sec IS NULL OR total_runtime_sec >= 0"`
	ClientTimestamp  time.Time  `gorm:"not null"`
	ServerReceivedAt time.Time  `gorm:"not null;index;index:idx_session_metric_hardware_received,priority:2;index:idx_session_metric_user_received,priority:2"`
	Diagnostics      string     `gorm:"type:jsonb;not null"`
}

type SessionMetricRuntime struct {
	HardwareID uuid.UUID `gorm:"type:uuid;primaryKey"`
	SessionID  uuid.UUID `gorm:"type:uuid;primaryKey"`
	RunTimeSec int64     `gorm:"not null;check:session_metric_runtime_nonnegative,run_time_sec >= 0"`
	UpdatedAt  time.Time `gorm:"not null"`
}

type SessionMetricDeviceRuntime struct {
	HardwareID      uuid.UUID `gorm:"type:uuid;primaryKey"`
	TotalRuntimeSec int64     `gorm:"not null;check:session_metric_device_runtime_nonnegative,total_runtime_sec >= 0"`
	UpdatedAt       time.Time `gorm:"not null"`
}
