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

const (
	MaxTrackingBatchSize   = 100
	PublicLocationPageSize = 100
	PublicLocationMaxAge   = 90 * time.Second
	PublicTrackMaxAge      = 20 * time.Minute
	PublicTrackMaxPoints   = 1200
)

type TrackingRepository interface {
	CreateSession(ctx context.Context, session *model.LiveTrackSession) error
	StorePoints(ctx context.Context, userID, sessionID uuid.UUID, points []model.LiveTrackPoint) (int64, error)
	StopSession(ctx context.Context, userID, sessionID uuid.UUID, endedAt time.Time) error
	ListActiveSessions(ctx context.Context, userID uuid.UUID) ([]model.LiveTrackSession, error)
	SetPublicSharing(ctx context.Context, userID, sessionID uuid.UUID, enabled bool) error
	DeleteLocationData(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	ListPublicLocations(ctx context.Context, userID, taskOwnerID uuid.UUID, south, north, west, east float64, after uuid.UUID, limit int, observedSince, observedUntil time.Time, includeOffline bool) ([]model.PublicPilotLocation, error)
	ListPublicTrackPoints(ctx context.Context, sessionIDs []uuid.UUID, observedSince, observedUntil time.Time, limitPerSession int) ([]model.PublicPilotTrackPoint, error)
}

type PilotFlightStateProvider interface {
	PilotFlightState(userID uuid.UUID) (flying bool, startedAt time.Time)
}

type TrackingService struct {
	tracking     TrackingRepository
	flightStates PilotFlightStateProvider
}

func NewTrackingService(tracking TrackingRepository, flightStates ...PilotFlightStateProvider) *TrackingService {
	service := &TrackingService{tracking: tracking}
	if len(flightStates) > 0 {
		service.flightStates = flightStates[0]
	}
	return service
}

func (s *TrackingService) StartSession(ctx context.Context, userID uuid.UUID, pilotName string, startedAt time.Time, publiclyShare bool) (*model.LiveTrackSession, error) {
	pilotName = strings.TrimSpace(pilotName)
	if userID == uuid.Nil || pilotName == "" || len([]rune(pilotName)) > 32 || startedAt.IsZero() || startedAt.After(time.Now().UTC().Add(5*time.Minute)) {
		return nil, ErrInvalidTrackingData
	}
	session := &model.LiveTrackSession{
		ID:            uuid.New(),
		UserID:        userID,
		PilotName:     pilotName,
		StartedAt:     startedAt.UTC(),
		PubliclyShare: publiclyShare,
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

func (s *TrackingService) SetPublicSharing(ctx context.Context, userID, sessionID uuid.UUID, enabled bool) error {
	if userID == uuid.Nil || sessionID == uuid.Nil {
		return ErrInvalidTrackingData
	}
	return s.tracking.SetPublicSharing(ctx, userID, sessionID, enabled)
}

func (s *TrackingService) DeleteLocationData(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	if userID == uuid.Nil {
		return nil, ErrInvalidTrackingData
	}
	return s.tracking.DeleteLocationData(ctx, userID)
}

func (s *TrackingService) ListPublicLocations(ctx context.Context, userID, taskOwnerID uuid.UUID, south, north, west, east float64, after uuid.UUID, includeOffline bool) ([]model.PublicPilotLocation, error) {
	if !finiteInRange(south, -90, 90) ||
		!finiteInRange(north, -90, 90) ||
		!finiteInRange(west, -180, 180) ||
		!finiteInRange(east, -180, 180) ||
		south >= north {
		return nil, ErrInvalidTrackingData
	}
	now := time.Now().UTC()
	observedUntil := now.Add(5 * time.Second)
	locations, err := s.tracking.ListPublicLocations(
		ctx,
		userID,
		taskOwnerID,
		south,
		north,
		west,
		east,
		after,
		PublicLocationPageSize,
		now.Add(-PublicLocationMaxAge),
		observedUntil,
		includeOffline,
	)
	if err != nil || len(locations) == 0 {
		return locations, err
	}

	sessionIDs := make([]uuid.UUID, 0, len(locations))
	locationIndexes := make(map[uuid.UUID]int, len(locations))
	var earliestFlightStart time.Time
	for index := range locations {
		location := &locations[index]
		location.Path = []model.PublicPilotTrackPoint{}
		roomID := location.SessionID
		location.LiveRoomID = &roomID
		if s.flightStates != nil {
			location.IsFlying, location.FlightStartedAt = s.flightStates.PilotFlightState(location.UserID)
		}
		if !location.IsFlying {
			continue
		}
		if location.FlightStartedAt.IsZero() {
			location.FlightStartedAt = location.StartedAt
		}
		if earliestFlightStart.IsZero() || location.FlightStartedAt.Before(earliestFlightStart) {
			earliestFlightStart = location.FlightStartedAt
		}
		sessionIDs = append(sessionIDs, roomID)
		locationIndexes[location.SessionID] = index
	}
	if len(sessionIDs) == 0 {
		return locations, nil
	}
	trackPoints, err := s.tracking.ListPublicTrackPoints(
		ctx,
		sessionIDs,
		earliestFlightStart,
		observedUntil,
		PublicTrackMaxPoints,
	)
	if err != nil {
		return nil, err
	}
	for _, point := range trackPoints {
		if index, ok := locationIndexes[point.SessionID]; ok && !point.Timestamp.Before(locations[index].FlightStartedAt) {
			locations[index].Path = append(locations[index].Path, point)
		}
	}
	return locations, nil
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
