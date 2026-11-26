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

type InstalledClient struct {
	HardwareID        uuid.UUID  `json:"hardware_id"`
	UserID            *uuid.UUID `json:"user_id,omitempty"`
	Username          string     `json:"username,omitempty"`
	AppVersion        string     `json:"app_version"`
	ClientIP          string     `json:"client_ip"`
	ClientTimestamp   time.Time  `json:"client_timestamp"`
	ServerReceivedAt  time.Time  `json:"server_received_at"`
	SessionCount      int64      `json:"session_count"`
	EventCount        int64      `json:"event_count"`
	TotalRuntimeSec   int64      `json:"total_runtime_sec"`
	LatestDiagnostics string     `json:"latest_diagnostics"`
}

type InstalledClientEvent struct {
	EventID          uuid.UUID  `json:"event_id"`
	SessionID        uuid.UUID  `json:"session_id"`
	UserID           *uuid.UUID `json:"user_id,omitempty"`
	AppVersion       string     `json:"app_version"`
	ClientIP         string     `json:"client_ip"`
	RunTimeSec       int64      `json:"run_time_sec"`
	TotalRuntimeSec  *int64     `json:"total_runtime_sec,omitempty"`
	ClientTimestamp  time.Time  `json:"client_timestamp"`
	ServerReceivedAt time.Time  `json:"server_received_at"`
	Diagnostics      string     `json:"diagnostics"`
}

type InstalledClientDetail struct {
	InstalledClient
	Events []InstalledClientEvent `json:"events"`
}
