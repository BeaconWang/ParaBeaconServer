package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type User struct {
	ID              uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Username        string    `gorm:"size:32;not null;uniqueIndex" json:"username"`
	DisplayName     string    `gorm:"size:48;not null;default:''" json:"display_name"`
	Introduction    string    `gorm:"size:200;not null;default:''" json:"introduction"`
	Gender          string    `gorm:"size:24;not null;default:''" json:"gender"`
	Birthday        string    `gorm:"size:10;not null;default:''" json:"birthday"`
	ChannelNumber   int       `gorm:"not null;default:0;index:idx_users_channel_number" json:"channel_number"`
	AvatarID        int       `gorm:"not null;default:0" json:"avatar_id"`
	ProfileImageKey string    `gorm:"size:36;not null;default:''" json:"-"`
	Email           *string   `gorm:"size:254;uniqueIndex" json:"email,omitempty"`
	PilotNumber     *int      `gorm:"-:migration" json:"-"`
	PasswordHash    string    `gorm:"size:255;not null" json:"-"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (u *User) BeforeCreate(_ *gorm.DB) error {
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	return nil
}
