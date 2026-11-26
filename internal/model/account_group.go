package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	PermissionAPKManagement          = "apk_management"
	PermissionXCRouteCustomization   = "xc_route_customization"
	PermissionAccountGroupManagement = "account_group_management"
)

type Permission struct {
	Code        string    `gorm:"size:64;primaryKey" json:"code"`
	Category    string    `gorm:"size:64;not null;index" json:"category"`
	DisplayName string    `gorm:"size:128;not null" json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}

type AccountGroup struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Name        string    `gorm:"size:64;not null;uniqueIndex" json:"name"`
	Description string    `gorm:"size:256;not null;default:''" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (g *AccountGroup) BeforeCreate(_ *gorm.DB) error {
	if g.ID == uuid.Nil {
		g.ID = uuid.New()
	}
	return nil
}

type AccountGroupMembership struct {
	UserID    uuid.UUID `gorm:"type:uuid;primaryKey" json:"user_id"`
	GroupID   uuid.UUID `gorm:"type:uuid;primaryKey" json:"group_id"`
	CreatedAt time.Time `json:"created_at"`
}

type AccountGroupPermission struct {
	GroupID        uuid.UUID `gorm:"type:uuid;primaryKey" json:"group_id"`
	PermissionCode string    `gorm:"size:64;primaryKey" json:"permission_code"`
	CreatedAt      time.Time `json:"created_at"`
}

type AccountGroupSummary struct {
	AccountGroup
	Permissions []Permission `json:"permissions"`
	MemberCount int64        `json:"member_count"`
}

type AccountGroupMember struct {
	ID       uuid.UUID   `json:"id"`
	Username string      `json:"username"`
	Email    *string     `json:"email,omitempty"`
	GroupIDs []uuid.UUID `json:"group_ids"`
}
