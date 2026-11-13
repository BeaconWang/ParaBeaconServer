package service

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/repository"
)

var (
	ErrInvalidTrackingData = errors.New("invalid tracking data")
	ErrTrackingNotFound    = repository.ErrTrackingSessionNotFound
)

const MaxTrackingBatchSize = 100

type TrackingRepository interface {
	CreateSession(ctx context.Context, session *model.LiveTrackSession) error
	StorePoints(ctx context.Context, userID, sessionID uuid.UUID, points []model.LiveTrackPoint) (int64, error)
	StopSession(ctx context.Context, userID, sessionID uuid.UUID, endedAt time.Time) error
	ListActiveSessions(ctx context.Context, userID uuid.UUID) ([]model.LiveTrackSession, error)
}

type TrackingService struct {
	tracking TrackingRepository
}

func NewTrackingService(tracking TrackingRepository) *TrackingService {
	return &TrackingService{tracking: tracking}
}

func (s *TrackingService) StartSession(ctx context.Context, userID uuid.UUID, pilotName string, startedAt time.Time) (*model.LiveTrackSession, error) {
	pilotName = strings.TrimSpace(pilotName)
	if userID == uuid.Nil || pilotName == "" || len([]rune(pilotName)) > 32 || startedAt.IsZero() || startedAt.After(time.Now().UTC().Add(5*time.Minute)) {
		return nil, ErrInvalidTrackingData
	}
	session := &model.LiveTrackSession{
		ID:        uuid.New(),
		UserID:    userID,
		PilotName: pilotName,
		StartedAt: startedAt.UTC(),
	}
	if err := s.tracking.CreateSession(ctx, session); err != nil {
		return nil, err
	}
	return session, nil
}

func (s *TrackingService) StorePoints(ctx context.Context, userID, sessionID uuid.UUID, points []model.LiveTrackPoint) (int64, error) {
	if userID == uuid.Nil || sessionID == uuid.Nil || len(points) < 1 || len(points) > MaxTrackingBatchSize {
		return 0, ErrInvalidTrackingData
	}
	for _, point := range points {
		if err := ValidateTrackingPoint(point); err != nil {
			return 0, err
		}
	}
	return s.tracking.StorePoints(ctx, userID, sessionID, points)
}

func (s *TrackingService) StopSession(ctx context.Context, userID, sessionID uuid.UUID, endedAt time.Time) error {
	if userID == uuid.Nil || sessionID == uuid.Nil || endedAt.IsZero() || endedAt.After(time.Now().UTC().Add(5*time.Minute)) {
		return ErrInvalidTrackingData
	}
	err := s.tracking.StopSession(ctx, userID, sessionID, endedAt.UTC())
	if errors.Is(err, repository.ErrTrackingEndBeforeStart) {
		return ErrInvalidTrackingData
	}
	return err
}

func (s *TrackingService) ListActiveSessions(ctx context.Context, userID uuid.UUID) ([]model.LiveTrackSession, error) {
	if userID == uuid.Nil {
		return nil, ErrInvalidTrackingData
	}
	return s.tracking.ListActiveSessions(ctx, userID)
}

func ValidateTrackingPoint(point model.LiveTrackPoint) error {
	if !finiteInRange(point.Latitude, -90, 90) ||
		!finiteInRange(point.Longitude, -180, 180) ||
		!finiteInRange(point.Altitude, -2000, 30000) ||
		!finiteInRange(point.GroundSpeed, 0, 200) ||
		point.Timestamp.IsZero() ||
		point.Timestamp.After(time.Now().UTC().Add(5*time.Minute)) {
		return ErrInvalidTrackingData
	}
	if !optionalInRange(point.VerticalSpeed, -100, 100) ||
		!optionalInRange(point.GPSAltitude, -2000, 30000) ||
		!optionalInRange(point.BarometricAltitude, -2000, 30000) ||
		!optionalDegrees(point.Heading) ||
		!optionalDegrees(point.Bearing) ||
		!optionalInRange(point.WindSpeed, 0, 200) ||
		!optionalDegrees(point.WindDirection) ||
		!optionalInRange(point.GPSAccuracy, 0, 10000) ||
		!optionalInRange(point.Pressure, 100, 1200) ||
		!optionalInRange(point.Temperature, -100, 100) ||
		(point.Satellites != nil && (*point.Satellites < 0 || *point.Satellites > 100)) {
		return ErrInvalidTrackingData
	}
	return nil
}

func finiteInRange(value, minimum, maximum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= minimum && value <= maximum
}

func optionalInRange(value *float64, minimum, maximum float64) bool {
	return value == nil || finiteInRange(*value, minimum, maximum)
}

func optionalDegrees(value *float64) bool {
	return value == nil || (finiteInRange(*value, 0, 360) && *value < 360)
}
