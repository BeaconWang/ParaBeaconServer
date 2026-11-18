package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
)

func TestValidateTrackingPoint(t *testing.T) {
	now := time.Now().UTC()
	valid := model.LiveTrackPoint{
		Latitude:    47.1,
		Longitude:   8.5,
		Altitude:    1200,
		GroundSpeed: 12.5,
		Timestamp:   now,
	}
	if err := ValidateTrackingPoint(valid); err != nil {
		t.Fatalf("valid point rejected: %v", err)
	}

	tests := map[string]model.LiveTrackPoint{
		"latitude out of range": func() model.LiveTrackPoint {
			point := valid
			point.Latitude = 91
			return point
		}(),
		"non-finite speed": func() model.LiveTrackPoint {
			point := valid
			point.GroundSpeed = math.Inf(1)
			return point
		}(),
		"invalid bearing": func() model.LiveTrackPoint {
			point := valid
			bearing := 360.0
			point.Bearing = &bearing
			return point
		}(),
		"missing timestamp": func() model.LiveTrackPoint {
			point := valid
			point.Timestamp = time.Time{}
			return point
		}(),
	}
	for name, point := range tests {
		t.Run(name, func(t *testing.T) {
			if err := ValidateTrackingPoint(point); err == nil {
				t.Fatal("expected invalid point to be rejected")
			}
		})
	}
}

type trackingTestRepository struct {
	created              *model.LiveTrackSession
	sharingUserID        uuid.UUID
	sharingID            uuid.UUID
	sharing              bool
	queryUserID          uuid.UUID
	bounds               [4]float64
	after                uuid.UUID
	limit                int
	observedSince        time.Time
	observedUntil        time.Time
	locations            []model.PublicPilotLocation
	listCalled           bool
	trackPointSessionIDs []uuid.UUID
	trackPointSince      time.Time
	trackPointUntil      time.Time
	trackPointLimit      int
	trackPoints          []model.PublicPilotTrackPoint
	trackPointsCalled    bool
}

func (r *trackingTestRepository) CreateSession(_ context.Context, session *model.LiveTrackSession) error {
	r.created = session
	return nil
}

func (r *trackingTestRepository) StorePoints(_ context.Context, _, _ uuid.UUID, _ []model.LiveTrackPoint) (int64, error) {
	return 0, nil
}

func (r *trackingTestRepository) StopSession(_ context.Context, _, _ uuid.UUID, _ time.Time) error {
	return nil
}

func (r *trackingTestRepository) ListActiveSessions(_ context.Context, _ uuid.UUID) ([]model.LiveTrackSession, error) {
	return nil, nil
}

func (r *trackingTestRepository) SetPublicSharing(_ context.Context, userID, sessionID uuid.UUID, enabled bool) error {
	r.sharingUserID = userID
	r.sharingID = sessionID
	r.sharing = enabled
	return nil
}

func (r *trackingTestRepository) ListPublicLocations(_ context.Context, userID uuid.UUID, south, north, west, east float64, after uuid.UUID, limit int, observedSince, observedUntil time.Time) ([]model.PublicPilotLocation, error) {
	r.queryUserID = userID
	r.listCalled = true
	r.bounds = [4]float64{south, north, west, east}
	r.after = after
	r.limit = limit
	r.observedSince = observedSince
	r.observedUntil = observedUntil
	return r.locations, nil
}

func (r *trackingTestRepository) ListPublicTrackPoints(_ context.Context, sessionIDs []uuid.UUID, observedSince, observedUntil time.Time, limitPerSession int) ([]model.PublicPilotTrackPoint, error) {
	r.trackPointsCalled = true
	r.trackPointSessionIDs = sessionIDs
	r.trackPointSince = observedSince
	r.trackPointUntil = observedUntil
	r.trackPointLimit = limitPerSession
	return r.trackPoints, nil
}

func TestStartSessionStoresExplicitPublicSharingConsent(t *testing.T) {
	repository := &trackingTestRepository{}
	tracking := NewTrackingService(repository)
	userID := uuid.New()
	startedAt := time.Now().UTC()

	session, err := tracking.StartSession(context.Background(), userID, "pilot_one", startedAt, true)
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	if repository.created == nil || !repository.created.PubliclyShare {
		t.Fatal("session did not retain explicit public-sharing consent")
	}
	if session.UserID != userID || session.PilotName != "pilot_one" {
		t.Fatalf("created session = %#v", session)
	}
}

func TestListPublicLocationsValidatesAndBoundsResults(t *testing.T) {
	repository := &trackingTestRepository{locations: []model.PublicPilotLocation{{PilotName: "pilot_one"}}}
	tracking := NewTrackingService(repository)
	after := uuid.New()
	requesterID := uuid.New()

	locations, err := tracking.ListPublicLocations(context.Background(), requesterID, -10, 10, 170, -170, after)
	if err != nil {
		t.Fatalf("ListPublicLocations() error = %v", err)
	}
	if len(locations) != 1 || !repository.listCalled {
		t.Fatalf("locations = %#v; repository called = %v", locations, repository.listCalled)
	}
	if repository.queryUserID != requesterID || repository.bounds != [4]float64{-10, 10, 170, -170} || repository.after != after {
		t.Fatalf("query bounds/cursor = %#v / %s", repository.bounds, repository.after)
	}
	if repository.limit != PublicLocationPageSize {
		t.Fatalf("page limit = %d, want %d", repository.limit, PublicLocationPageSize)
	}
	now := time.Now().UTC()
	if repository.observedSince.Before(now.Add(-PublicLocationMaxAge-time.Second)) ||
		repository.observedSince.After(now.Add(-PublicLocationMaxAge+time.Second)) ||
		repository.observedUntil.Before(now) ||
		repository.observedUntil.After(now.Add(6*time.Second)) {
		t.Fatalf("freshness window = %s..%s", repository.observedSince, repository.observedUntil)
	}
}

func TestListPublicLocationsAttachesOnlyBoundedPublicTrailData(t *testing.T) {
	sessionID := uuid.New()
	repository := &trackingTestRepository{
		locations: []model.PublicPilotLocation{{SessionID: sessionID, PilotName: "pilot_one"}},
		trackPoints: []model.PublicPilotTrackPoint{{
			SessionID:   sessionID,
			Latitude:    47.1,
			Longitude:   8.5,
			Altitude:    1200,
			GroundSpeed: 12,
			Timestamp:   time.Now().UTC(),
		}},
	}
	tracking := NewTrackingService(repository)

	locations, err := tracking.ListPublicLocations(context.Background(), uuid.New(), -10, 10, 170, -170, uuid.Nil)
	if err != nil {
		t.Fatalf("ListPublicLocations() error = %v", err)
	}
	if !repository.trackPointsCalled || len(repository.trackPointSessionIDs) != 1 || repository.trackPointSessionIDs[0] != sessionID {
		t.Fatalf("track point query sessions = %v", repository.trackPointSessionIDs)
	}
	if repository.trackPointLimit != PublicTrackMaxPoints {
		t.Fatalf("track point limit = %d, want %d", repository.trackPointLimit, PublicTrackMaxPoints)
	}
	if len(locations) != 1 || len(locations[0].Path) != 1 || locations[0].Path[0].SessionID != sessionID {
		t.Fatalf("public pilot path = %#v", locations)
	}
	now := time.Now().UTC()
	if repository.trackPointSince.Before(now.Add(-PublicTrackMaxAge-time.Second)) ||
		repository.trackPointSince.After(now.Add(-PublicTrackMaxAge+time.Second)) {
		t.Fatalf("track point history starts at %s", repository.trackPointSince)
	}
}

func TestListPublicLocationsRejectsInvalidBounds(t *testing.T) {
	repository := &trackingTestRepository{}
	tracking := NewTrackingService(repository)
	if _, err := tracking.ListPublicLocations(context.Background(), uuid.New(), 20, 10, 0, 1, uuid.Nil); err != ErrInvalidTrackingData {
		t.Fatalf("invalid bounds error = %v, want ErrInvalidTrackingData", err)
	}
	if repository.listCalled {
		t.Fatal("repository called with invalid bounds")
	}
}

func TestSetPublicSharingPassesOwnerAndSession(t *testing.T) {
	repository := &trackingTestRepository{}
	tracking := NewTrackingService(repository)
	userID, sessionID := uuid.New(), uuid.New()
	if err := tracking.SetPublicSharing(context.Background(), userID, sessionID, true); err != nil {
		t.Fatalf("SetPublicSharing() error = %v", err)
	}
	if repository.sharingUserID != userID || repository.sharingID != sessionID || !repository.sharing {
		t.Fatalf("sharing update = %s / %s / %v", repository.sharingUserID, repository.sharingID, repository.sharing)
	}
}
