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

type CompetitionTaskListEntry struct {
	UserID      uuid.UUID `gorm:"column:user_id"`
	TaskData    string    `gorm:"column:task_data"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
	Username    string    `gorm:"column:username"`
	DisplayName string    `gorm:"column:display_name"`
}
