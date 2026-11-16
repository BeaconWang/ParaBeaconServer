package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
)

var ErrInvalidLandingPoints = errors.New("invalid landing points")

const MaxLandingPoints = 100

type LandingPointRepository interface {
	Merge(ctx context.Context, userID uuid.UUID, points []model.LandingPoint, defaultID *string) error
	List(ctx context.Context, userID uuid.UUID) ([]model.LandingPoint, *string, error)
}

type LandingPointService struct {
	points LandingPointRepository
}

func NewLandingPointService(points LandingPointRepository) *LandingPointService {
	return &LandingPointService{points: points}
}

func (s *LandingPointService) Import(ctx context.Context, userID uuid.UUID, points []model.LandingPoint, defaultID *string) error {
	if userID == uuid.Nil || len(points) > MaxLandingPoints {
		return ErrInvalidLandingPoints
	}
	ids := make(map[string]struct{}, len(points))
	for _, point := range points {
		if !validLandingPoint(point) {
			return ErrInvalidLandingPoints
		}
		if _, exists := ids[point.ClientID]; exists {
			return ErrInvalidLandingPoints
		}
		ids[point.ClientID] = struct{}{}
	}
	if defaultID != nil {
		if _, exists := ids[*defaultID]; !exists {
			return ErrInvalidLandingPoints
		}
	}
	return s.points.Merge(ctx, userID, points, defaultID)
}

func (s *LandingPointService) List(ctx context.Context, userID uuid.UUID) ([]model.LandingPoint, *string, error) {
	if userID == uuid.Nil {
		return nil, nil, ErrInvalidLandingPoints
	}
	points, defaultID, err := s.points.List(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if len(points) > MaxLandingPoints {
		return nil, nil, ErrInvalidLandingPoints
	}
	return points, defaultID, nil
}

func validLandingPoint(point model.LandingPoint) bool {
	name := strings.TrimSpace(point.Name)
	if point.ClientID == "" || len([]rune(point.ClientID)) > 100 || hasControl(point.ClientID) ||
		name == "" || len([]rune(point.Name)) > 60 || hasControl(point.Name) ||
		!finiteInRange(point.Latitude, -90, 90) ||
		!finiteInRange(point.Longitude, -180, 180) ||
		point.CreatedAt.IsZero() || point.CreatedAt.After(time.Now().UTC().Add(5*time.Minute)) ||
		len([]rune(point.Notes)) > 500 || hasControl(point.Notes) {
		return false
	}
	return point.AltitudeM == nil || finiteInRange(*point.AltitudeM, -12000, 100000)
}

func hasControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
