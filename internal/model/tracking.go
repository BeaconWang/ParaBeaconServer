package model

import (
	"time"

	"github.com/google/uuid"
)

type LiveTrackSession struct {
	ID            uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	UserID        uuid.UUID  `gorm:"type:uuid;not null;index" json:"-"`
	PilotName     string     `gorm:"size:32;not null" json:"pilot_name"`
	StartedAt     time.Time  `gorm:"not null;index" json:"started_at"`
	EndedAt       *time.Time `gorm:"index" json:"ended_at,omitempty"`
	PubliclyShare bool       `gorm:"not null;default:false;index" json:"-"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type PublicPilotLocation struct {
	SessionID     uuid.UUID               `gorm:"column:session_id" json:"-"`
	LiveRoomID    *uuid.UUID              `gorm:"-" json:"live_room_id,omitempty"`
	PilotNumber   int                     `gorm:"column:pilot_number" json:"pilot_number"`
	PilotName     string                  `gorm:"column:pilot_name" json:"pilot_name"`
	Latitude      float64                 `gorm:"column:latitude" json:"lat"`
	Longitude     float64                 `gorm:"column:longitude" json:"lon"`
	Altitude      float64                 `gorm:"column:altitude" json:"altitude"`
	GroundSpeed   float64                 `gorm:"column:ground_speed" json:"speed"`
	VerticalSpeed *float64                `gorm:"column:vertical_speed" json:"vertical_speed,omitempty"`
	Heading       *float64                `gorm:"column:heading" json:"heading,omitempty"`
	ObservedAt    time.Time               `gorm:"column:observed_at" json:"observed_at"`
	Path          []PublicPilotTrackPoint `gorm:"-" json:"path"`
}

type PublicPilotTrackPoint struct {
	SessionID     uuid.UUID `gorm:"column:session_id" json:"-"`
	Latitude      float64   `gorm:"column:latitude" json:"lat"`
	Longitude     float64   `gorm:"column:longitude" json:"lon"`
	Altitude      float64   `gorm:"column:altitude" json:"altitude"`
	GroundSpeed   float64   `gorm:"column:ground_speed" json:"speed"`
	VerticalSpeed *float64  `gorm:"column:vertical_speed" json:"vertical_speed,omitempty"`
	Heading       *float64  `gorm:"column:heading" json:"heading,omitempty"`
	Timestamp     time.Time `gorm:"column:timestamp" json:"timestamp"`
}

type LiveTrackPoint struct {
	ID                 uint64    `gorm:"primaryKey" json:"-"`
	SessionID          uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_live_track_session_timestamp,priority:1;index" json:"session_id"`
	UserID             uuid.UUID `gorm:"type:uuid;not null;index" json:"-"`
	Latitude           float64   `gorm:"not null" json:"lat"`
	Longitude          float64   `gorm:"not null" json:"lon"`
	Altitude           float64   `gorm:"not null" json:"altitude"`
	GroundSpeed        float64   `gorm:"not null" json:"speed"`
	VerticalSpeed      *float64  `json:"vertical_speed,omitempty"`
	GPSAltitude        *float64  `json:"gps_altitude,omitempty"`
	BarometricAltitude *float64  `json:"barometric_altitude,omitempty"`
	Heading            *float64  `json:"heading,omitempty"`
	Bearing            *float64  `json:"bearing,omitempty"`
	WindSpeed          *float64  `json:"wind_speed,omitempty"`
	WindDirection      *float64  `json:"wind_direction,omitempty"`
	GPSAccuracy        *float64  `json:"gps_accuracy,omitempty"`
	Satellites         *int      `json:"satellites,omitempty"`
	Pressure           *float64  `json:"pressure,omitempty"`
	Temperature        *float64  `json:"temperature,omitempty"`
	Timestamp          time.Time `gorm:"not null;uniqueIndex:idx_live_track_session_timestamp,priority:2;index" json:"timestamp"`
	CreatedAt          time.Time `json:"created_at"`
}
