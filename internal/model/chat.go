package model

import (
	"time"

	"github.com/google/uuid"
)

type ChatMessage struct {
	ID                   uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	Scope                string     `gorm:"size:16;not null;index:idx_chat_channel_created,priority:1;index:idx_chat_sender_created,priority:1" json:"scope"`
	ChannelNumber        *int       `gorm:"index:idx_chat_channel_created,priority:2" json:"channel_number,omitempty"`
	SenderID             uuid.UUID  `gorm:"type:uuid;not null;index:idx_chat_sender_created,priority:1" json:"-"`
	SenderPilotNumber    int        `gorm:"not null;index:idx_chat_sender_created,priority:2" json:"sender_pilot_number"`
	SenderName           string     `gorm:"size:32;not null" json:"sender_name"`
	SenderAvatarID       int        `gorm:"not null;default:0" json:"sender_avatar_id"`
	RecipientID          *uuid.UUID `gorm:"type:uuid;index:idx_chat_recipient_created,priority:1" json:"-"`
	RecipientPilotNumber *int       `gorm:"index:idx_chat_recipient_created,priority:2" json:"recipient_pilot_number,omitempty"`
	Text                 string     `gorm:"type:text;not null" json:"text"`
	CreatedAt            time.Time  `gorm:"not null;index:idx_chat_channel_created,priority:3;index:idx_chat_sender_created,priority:3;index:idx_chat_recipient_created,priority:3" json:"created_at"`
}
