package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
)

type landingPointTestRepository struct {
	userID    uuid.UUID
	points    []model.LandingPoint
	defaultID *string
}

func (r *landingPointTestRepository) Merge(_ context.Context, userID uuid.UUID, points []model.LandingPoint, defaultID *string) error {
	r.userID, r.points, r.defaultID = userID, points, defaultID
	return nil
}

func (r *landingPointTestRepository) List(_ context.Context, userID uuid.UUID) ([]model.LandingPoint, *string, error) {
	r.userID = userID
	return r.points, r.defaultID, nil
}

func TestLandingPointServiceImportsPrivateValidatedPoints(t *testing.T) {
	repository := &landingPointTestRepository{}
	service := NewLandingPointService(repository)
	userID := uuid.New()
	defaultID := "device-point-1"
	point := model.LandingPoint{
		ClientID: "device-point-1",
		Name: "Landing",
		Latitude: 47.1,
		Longitude: 8.5,
		CreatedAt: time.Now().UTC(),
		Notes: "Safe landing",
	}
	if err := service.Import(context.Background(), userID, []model.LandingPoint{point}, &defaultID); err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if repository.userID != userID || len(repository.points) != 1 || repository.defaultID == nil || *repository.defaultID != defaultID {
		t.Fatalf("imported points = %#v for user %s default=%v", repository.points, repository.userID, repository.defaultID)
	}
}

func TestLandingPointServiceRejectsInvalidPointsAndDefaults(t *testing.T) {
	repository := &landingPointTestRepository{}
	service := NewLandingPointService(repository)
	userID := uuid.New()
	valid := model.LandingPoint{ClientID: "id", Name: "Landing", Latitude: 47, Longitude: 8, CreatedAt: time.Now().UTC()}
	invalidPoints := []model.LandingPoint{
		{ClientID: "id", Name: "", Latitude: 47, Longitude: 8, CreatedAt: valid.CreatedAt},
		{ClientID: "id", Name: "Landing", Latitude: 91, Longitude: 8, CreatedAt: valid.CreatedAt},
		{ClientID: "id", Name: "Landing", Latitude: 47, Longitude: 8, CreatedAt: valid.CreatedAt, Notes: "bad\nnotes"},
	}
	for _, point := range invalidPoints {
		if err := service.Import(context.Background(), userID, []model.LandingPoint{point}, nil); err != ErrInvalidLandingPoints {
			t.Errorf("Import(%#v) error = %v, want ErrInvalidLandingPoints", point, err)
		}
	}
	missingDefault := "not-in-bundle"
	if err := service.Import(context.Background(), userID, []model.LandingPoint{valid}, &missingDefault); err != ErrInvalidLandingPoints {
		t.Fatalf("invalid default error = %v, want ErrInvalidLandingPoints", err)
	}
	if len(repository.points) != 0 {
		t.Fatalf("invalid points reached repository: %#v", repository.points)
	}
}
