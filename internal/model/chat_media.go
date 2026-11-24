package model

import (
	"time"

	"github.com/google/uuid"
)

type ChatMedia struct {
	ID            uuid.UUID  `gorm:"type:uuid;primaryKey" json:"-"`
	SenderID      uuid.UUID  `gorm:"type:uuid;not null;index" json:"-"`
	Scope         string     `gorm:"size:16;not null" json:"-"`
	ChannelNumber *int       `json:"-"`
	RecipientID   *uuid.UUID `gorm:"type:uuid" json:"-"`
	Kind          string     `gorm:"size:16;not null" json:"-"`
	ContentType   string     `gorm:"size:64;not null" json:"-"`
	StorageKey    string     `gorm:"size:64;not null;uniqueIndex" json:"-"`
	MessageID     *uuid.UUID `gorm:"type:uuid;uniqueIndex" json:"-"`
	ExpiresAt     time.Time  `gorm:"not null;index" json:"-"`
	CreatedAt     time.Time  `gorm:"not null;index" json:"-"`
}
