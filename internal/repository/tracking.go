package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrTrackingSessionNotFound = errors.New("tracking session not found")
	ErrTrackingEndBeforeStart  = errors.New("tracking end time precedes session start")
)

type TrackingRepository interface {
	CreateSession(ctx context.Context, session *model.LiveTrackSession) error
	StorePoints(ctx context.Context, userID, sessionID uuid.UUID, points []model.LiveTrackPoint) (int64, error)
	StopSession(ctx context.Context, userID, sessionID uuid.UUID, endedAt time.Time) error
	ListActiveSessions(ctx context.Context, userID uuid.UUID) ([]model.LiveTrackSession, error)
	SetPublicSharing(ctx context.Context, userID, sessionID uuid.UUID, enabled bool) error
	ListPublicLocations(ctx context.Context, userID uuid.UUID, south, north, west, east float64, after uuid.UUID, limit int, observedSince, observedUntil time.Time) ([]model.PublicPilotLocation, error)
}

type GormTrackingRepository struct {
	db *gorm.DB
}

func NewTrackingRepository(db *gorm.DB) *GormTrackingRepository {
	return &GormTrackingRepository{db: db}
}

func (r *GormTrackingRepository) CreateSession(ctx context.Context, session *model.LiveTrackSession) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		endedAt := time.Now().UTC()
		if err := tx.Model(&model.LiveTrackSession{}).
			Where("user_id = ? AND ended_at IS NULL", session.UserID).
			Update("ended_at", endedAt).Error; err != nil {
			return err
		}
		return tx.Create(session).Error
	})
}

func (r *GormTrackingRepository) StorePoints(ctx context.Context, userID, sessionID uuid.UUID, points []model.LiveTrackPoint) (int64, error) {
	var accepted int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session model.LiveTrackSession
		if err := tx.Where("id = ? AND user_id = ? AND ended_at IS NULL", sessionID, userID).First(&session).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTrackingSessionNotFound
			}
			return err
		}

		for index := range points {
			points[index].SessionID = sessionID
			points[index].UserID = userID
		}
		result := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "session_id"}, {Name: "timestamp"}},
			DoNothing: true,
		}).CreateInBatches(&points, 100)
		if result.Error != nil {
			return result.Error
		}
		accepted = result.RowsAffected
		return nil
	})
	return accepted, err
}

func (r *GormTrackingRepository) StopSession(ctx context.Context, userID, sessionID uuid.UUID, endedAt time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session model.LiveTrackSession
		if err := tx.Where("id = ? AND user_id = ?", sessionID, userID).First(&session).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTrackingSessionNotFound
			}
			return err
		}
		if session.EndedAt != nil {
			return nil
		}
		if endedAt.Before(session.StartedAt) {
			return ErrTrackingEndBeforeStart
		}
		return tx.Model(&session).Update("ended_at", endedAt).Error
	})
}

func (r *GormTrackingRepository) SetPublicSharing(ctx context.Context, userID, sessionID uuid.UUID, enabled bool) error {
	result := r.db.WithContext(ctx).
		Model(&model.LiveTrackSession{}).
		Where("id = ? AND user_id = ? AND ended_at IS NULL", sessionID, userID).
		Update("publicly_share", enabled)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTrackingSessionNotFound
	}
	return nil
}

func (r *GormTrackingRepository) ListPublicLocations(ctx context.Context, userID uuid.UUID, south, north, west, east float64, after uuid.UUID, limit int, observedSince, observedUntil time.Time) ([]model.PublicPilotLocation, error) {
	locations := make([]model.PublicPilotLocation, 0, limit)
	query := r.db.WithContext(ctx).
		Table("live_track_sessions AS s").
		Select("s.id AS session_id, s.pilot_name, latest.latitude, latest.longitude, latest.heading, latest.timestamp AS observed_at").
		Joins("JOIN LATERAL (SELECT p.latitude, p.longitude, p.heading, p.timestamp FROM live_track_points p WHERE p.session_id = s.id ORDER BY p.timestamp DESC LIMIT 1) AS latest ON TRUE").
		Where("s.ended_at IS NULL AND s.publicly_share = ?", true).
		Where("latest.timestamp >= ? AND latest.timestamp <= ?", observedSince, observedUntil).
		Where("latest.latitude BETWEEN ? AND ?", south, north)
	if userID != uuid.Nil {
		query = query.Where("s.user_id <> ?", userID)
	}
	if west <= east {
		query = query.Where("latest.longitude BETWEEN ? AND ?", west, east)
	} else {
		query = query.Where("(latest.longitude >= ? OR latest.longitude <= ?)", west, east)
	}
	if after != uuid.Nil {
		query = query.Where("s.id > ?", after)
	}
	if err := query.Order("s.id ASC").Limit(limit).Scan(&locations).Error; err != nil {
		return nil, err
	}
	return locations, nil
}

func (r *GormTrackingRepository) ListActiveSessions(ctx context.Context, userID uuid.UUID) ([]model.LiveTrackSession, error) {
	var sessions []model.LiveTrackSession
	if err := r.db.WithContext(ctx).
		Where("user_id = ? AND ended_at IS NULL", userID).
		Order("started_at DESC").
		Find(&sessions).Error; err != nil {
		return nil, err
	}
	return sessions, nil
}
