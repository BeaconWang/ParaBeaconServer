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

type trackingFlightState struct {
	flying    bool
	startedAt time.Time
}

func (s trackingFlightState) PilotFlightState(uuid.UUID) (bool, time.Time) {
	return s.flying, s.startedAt
}

type trackingTestRepository struct {
	created              *model.LiveTrackSession
	sharingUserID        uuid.UUID
	sharingID            uuid.UUID
	sharing              bool
	queryUserID          uuid.UUID
	taskOwnerID          uuid.UUID
	bounds               [4]float64
	after                uuid.UUID
	limit                int
	observedSince        time.Time
	observedUntil        time.Time
	includeOffline       bool
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

func (r *trackingTestRepository) ListPublicLocations(_ context.Context, userID, taskOwnerID uuid.UUID, south, north, west, east float64, after uuid.UUID, limit int, observedSince, observedUntil time.Time, includeOffline bool) ([]model.PublicPilotLocation, error) {
	r.queryUserID = userID
	r.taskOwnerID = taskOwnerID
	r.listCalled = true
	r.bounds = [4]float64{south, north, west, east}
	r.after = after
	r.limit = limit
	r.observedSince = observedSince
	r.observedUntil = observedUntil
	r.includeOffline = includeOffline
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

func TestListPublicLocationsCombinesViewerChannelAndPinnedTaskFilters(t *testing.T) {
	repository := &trackingTestRepository{locations: []model.PublicPilotLocation{{PilotName: "pilot_one", PilotNumber: 123456}}}
	tracking := NewTrackingService(repository)
	after := uuid.New()
	requesterID := uuid.New()
	taskOwnerID := uuid.New()

	locations, err := tracking.ListPublicLocations(context.Background(), requesterID, taskOwnerID, -10, 10, 170, -170, after, false)
	if err != nil {
		t.Fatalf("ListPublicLocations() error = %v", err)
	}
	if len(locations) != 1 || !repository.listCalled {
		t.Fatalf("locations = %#v; repository called = %v", locations, repository.listCalled)
	}
	if repository.queryUserID != requesterID || repository.taskOwnerID != taskOwnerID || repository.bounds != [4]float64{-10, 10, 170, -170} || repository.after != after {
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

func TestListPublicLocationsAttachesFlightTrailForOfflinePilot(t *testing.T) {
	sessionID := uuid.New()
	userID := uuid.New()
	flightStartedAt := time.Now().UTC().Add(-time.Minute)
	repository := &trackingTestRepository{
		locations: []model.PublicPilotLocation{{SessionID: sessionID, UserID: userID, StartedAt: flightStartedAt.Add(-time.Minute), PilotNumber: 123456, PilotName: "pilot_one", IsOnline: false}},
		trackPoints: []model.PublicPilotTrackPoint{{
			SessionID:   sessionID,
			Latitude:    47.1,
			Longitude:   8.5,
			Altitude:    1200,
			GroundSpeed: 12,
			Timestamp:   flightStartedAt.Add(time.Second),
		}},
	}
	tracking := NewTrackingService(repository, trackingFlightState{flying: true, startedAt: flightStartedAt})

	locations, err := tracking.ListPublicLocations(context.Background(), uuid.New(), uuid.Nil, -10, 10, 170, -170, uuid.Nil, true)
	if err != nil {
		t.Fatalf("ListPublicLocations() error = %v", err)
	}
	if !repository.trackPointsCalled || len(repository.trackPointSessionIDs) != 1 || repository.trackPointSessionIDs[0] != sessionID {
		t.Fatalf("track point query sessions = %v", repository.trackPointSessionIDs)
	}
	if repository.trackPointLimit != PublicTrackMaxPoints {
		t.Fatalf("track point limit = %d, want %d", repository.trackPointLimit, PublicTrackMaxPoints)
	}
	if !repository.includeOffline || len(locations) != 1 || !locations[0].IsFlying || locations[0].IsOnline || len(locations[0].Path) != 1 || locations[0].Path[0].SessionID != sessionID {
		t.Fatalf("offline pilot flight path = %#v", locations)
	}
	if !repository.trackPointSince.Equal(flightStartedAt) {
		t.Fatalf("track point history starts at %s, want %s", repository.trackPointSince, flightStartedAt)
	}
}

func TestListPublicLocationsRejectsInvalidBounds(t *testing.T) {
	repository := &trackingTestRepository{}
	tracking := NewTrackingService(repository)
	if _, err := tracking.ListPublicLocations(context.Background(), uuid.New(), uuid.Nil, 20, 10, 0, 1, uuid.Nil, false); err != ErrInvalidTrackingData {
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
