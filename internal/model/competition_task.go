package model

import (
	"time"

	"github.com/google/uuid"
)

type CompetitionTask struct {
	UserID    uuid.UUID `gorm:"type:uuid;primaryKey" json:"-"`
	TaskData  string    `gorm:"type:jsonb;not null" json:"-"`
	UpdatedAt time.Time `gorm:"not null;autoCreateTime;autoUpdateTime" json:"-"`
}
