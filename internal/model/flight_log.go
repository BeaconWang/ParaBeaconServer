package model

import (
	"time"

	"github.com/google/uuid"
)

type FlightLog struct {
	ID         uuid.UUID  `gorm:"type:uuid;primaryKey"`
	UserID     uuid.UUID  `gorm:"type:uuid;not null;uniqueIndex:idx_flight_log_identity,priority:1;index"`
	StartedAt  time.Time  `gorm:"not null;uniqueIndex:idx_flight_log_identity,priority:2;index"`
	PointCount int        `gorm:"not null;uniqueIndex:idx_flight_log_identity,priority:3"`
	Data       string     `gorm:"type:jsonb;not null"`
	ArchiveID  *uuid.UUID `gorm:"type:uuid;uniqueIndex" json:"-"`
	CreatedAt  time.Time  `json:"created_at"`
}
