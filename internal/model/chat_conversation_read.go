package model

import (
	"time"

	"github.com/google/uuid"
)

type ChatConversationRead struct {
	OwnerID    uuid.UUID `gorm:"type:uuid;primaryKey" json:"-"`
	PeerID     uuid.UUID `gorm:"type:uuid;primaryKey" json:"-"`
	LastReadAt time.Time `gorm:"not null" json:"last_read_at"`
}
