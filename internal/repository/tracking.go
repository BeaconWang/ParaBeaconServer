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
