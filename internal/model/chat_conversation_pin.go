package model

import (
	"time"

	"github.com/google/uuid"
)

type ChatConversationPin struct {
	OwnerID   uuid.UUID `gorm:"type:uuid;primaryKey;index:idx_chat_pins_owner_order,priority:1" json:"-"`
	PeerID    uuid.UUID `gorm:"type:uuid;primaryKey" json:"-"`
	PinnedAt  time.Time `gorm:"not null;index:idx_chat_pins_owner_order,priority:2" json:"pinned_at"`
	CreatedAt time.Time `gorm:"not null" json:"-"`
}
