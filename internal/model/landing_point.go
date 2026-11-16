package model

import (
	"time"

	"github.com/google/uuid"
)

type LandingPoint struct {
	UserID    uuid.UUID `gorm:"type:uuid;primaryKey" json:"-"`
	ClientID  string    `gorm:"size:100;primaryKey" json:"id"`
	Name      string    `gorm:"size:60;not null" json:"name"`
	Latitude  float64   `gorm:"not null" json:"latitude"`
	Longitude float64   `gorm:"not null" json:"longitude"`
	AltitudeM *float64  `json:"altitudeM"`
	CreatedAt time.Time `gorm:"not null;autoCreateTime:false" json:"createdAt"`
	Notes     string    `gorm:"size:500;not null;default:''" json:"notes"`
	StoredAt  time.Time `gorm:"autoCreateTime" json:"-"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"-"`
}

type LandingPointSettings struct {
	UserID    uuid.UUID `gorm:"type:uuid;primaryKey" json:"-"`
	DefaultID *string   `gorm:"size:100" json:"default_id"`
	UpdatedAt time.Time `json:"-"`
}
