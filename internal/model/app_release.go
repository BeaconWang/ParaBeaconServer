package model

import "time"

type AppRelease struct {
	VersionCode   int64      `gorm:"primaryKey;autoIncrement:false" json:"version_code"`
	VersionName   string     `gorm:"size:64;not null" json:"version_name"`
	PackageName   string     `gorm:"size:255;not null;index:idx_app_release_latest,priority:1" json:"package_name"`
	StorageKey    string     `gorm:"size:64;not null;uniqueIndex" json:"-"`
	SHA256        string     `gorm:"size:64;not null" json:"sha256"`
	SizeBytes     int64      `gorm:"not null" json:"size_bytes"`
	DownloadCount int64      `gorm:"not null;default:0" json:"download_count"`
	Notes         string     `gorm:"type:text;not null;default:''" json:"notes"`
	PublishedAt   *time.Time `gorm:"index:idx_app_release_latest,priority:2" json:"published_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}
