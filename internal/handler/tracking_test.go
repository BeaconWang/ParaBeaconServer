package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/middleware"
	"github.com/parabeacon/server/internal/model"
)

type trackingHandlerTestService struct {
	userID        uuid.UUID
	sessionID     uuid.UUID
	publiclyShare bool
	south         float64
	north         float64
	west          float64
	east          float64
	after         uuid.UUID
	taskOwnerID   uuid.UUID
	locations     []model.PublicPilotLocation
}

func (s *trackingHandlerTestService) StartSession(_ context.Context, userID uuid.UUID, pilotName string, startedAt time.Time, publiclyShare bool) (*model.LiveTrackSession, error) {
	s.userID = userID
	s.publiclyShare = publiclyShare
	return &model.LiveTrackSession{ID: uuid.New(), UserID: userID, PilotName: pilotName, StartedAt: startedAt, PubliclyShare: publiclyShare}, nil
}

func (*trackingHandlerTestService) StorePoints(context.Context, uuid.UUID, uuid.UUID, []model.LiveTrackPoint) (int64, error) {
	return 0, nil
}

func (*trackingHandlerTestService) StopSession(context.Context, uuid.UUID, uuid.UUID, time.Time) error {
	return nil
}

func (*trackingHandlerTestService) ListActiveSessions(context.Context, uuid.UUID) ([]model.LiveTrackSession, error) {
	return nil, nil
}

func (s *trackingHandlerTestService) SetPublicSharing(_ context.Context, userID, sessionID uuid.UUID, enabled bool) error {
	s.userID = userID
	s.sessionID = sessionID
	s.publiclyShare = enabled
	return nil
}

func (s *trackingHandlerTestService) ListPublicLocations(_ context.Context, userID, taskOwnerID uuid.UUID, south, north, west, east float64, after uuid.UUID) ([]model.PublicPilotLocation, error) {
	s.userID = userID
	s.taskOwnerID = taskOwnerID
	s.south, s.north, s.west, s.east, s.after = south, north, west, east, after
	return s.locations, nil
}

func TestListPublicLocationsRequiresBoundsAndReturnsSharedFlightData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sessionID := uuid.New()
	service := &trackingHandlerTestService{
		locations: []model.PublicPilotLocation{{
			SessionID:       sessionID,
			PilotNumber:     123456,
			ChannelNumber:   456789,
			AvatarID:        3,
			ProfileImageURL: "/api/v1/profile-images/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
			PilotName:       "Sky Rider",
			Latitude:        47.1,
			Longitude:       8.5,
			Altitude:        1200,
			GroundSpeed:     12,
			ObservedAt:      time.Now().UTC(),
			Path: []model.PublicPilotTrackPoint{{
				SessionID:   sessionID,
				Latitude:    47.1,
				Longitude:   8.5,
				Altitude:    1200,
				GroundSpeed: 12,
				Timestamp:   time.Now().UTC(),
			}},
		}},
	}
	userID := uuid.New()
	engine := gin.New()
	engine.GET("/map/active-locations", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		NewTrackingHandler(service).ListPublicLocations(c)
	})
	taskOwnerID := uuid.New()
	request := httptest.NewRequest(http.MethodGet, "/map/active-locations?west=8&south=47&east=9&north=48&task_owner_id="+taskOwnerID.String(), nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if service.userID != userID {
		t.Fatalf("requester id = %s, want %s", service.userID, userID)
	}
	if service.taskOwnerID != taskOwnerID {
		t.Fatalf("task owner filter = %s, want %s", service.taskOwnerID, taskOwnerID)
	}
	if service.south != 47 || service.north != 48 || service.west != 8 || service.east != 9 {
		t.Fatalf("bounds = %v %v %v %v", service.south, service.north, service.west, service.east)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var locations []map[string]json.RawMessage
	if err := json.Unmarshal(payload["locations"], &locations); err != nil {
		t.Fatalf("decode locations: %v", err)
	}
	if len(locations) != 1 {
		t.Fatalf("location count = %d, want 1", len(locations))
	}
	for _, privateField := range []string{"session_id", "user_id", "id", "gps_accuracy", "pressure"} {
		if _, exists := locations[0][privateField]; exists {
			t.Errorf("public location unexpectedly contains %q", privateField)
		}
	}
	var publicPilotName string
	if err := json.Unmarshal(locations[0]["pilot_name"], &publicPilotName); err != nil {
		t.Fatalf("decode public pilot name: %v", err)
	}
	if publicPilotName != "Sky Rider" {
		t.Fatalf("public pilot name = %q, want display name", publicPilotName)
	}
	var publicChannelNumber int
	if err := json.Unmarshal(locations[0]["channel_number"], &publicChannelNumber); err != nil || publicChannelNumber != 456789 {
		t.Fatalf("public channel number = %d, err=%v", publicChannelNumber, err)
	}
	var publicAvatarID int
	if err := json.Unmarshal(locations[0]["avatar_id"], &publicAvatarID); err != nil || publicAvatarID != 3 {
		t.Fatalf("public avatar id = %d, err=%v", publicAvatarID, err)
	}
	var publicProfileImageURL string
	if err := json.Unmarshal(locations[0]["profile_image_url"], &publicProfileImageURL); err != nil || !strings.Contains(publicProfileImageURL, "/api/v1/profile-images/") {
		t.Fatalf("public profile image URL = %q, err=%v", publicProfileImageURL, err)
	}
	for _, sharedField := range []string{"pilot_name", "pilot_number", "channel_number", "avatar_id", "profile_image_url", "altitude", "speed", "path"} {
		if _, exists := locations[0][sharedField]; !exists {
			t.Errorf("public location has no shared flight field %q", sharedField)
		}
	}
	var path []map[string]json.RawMessage
	if err := json.Unmarshal(locations[0]["path"], &path); err != nil {
		t.Fatalf("decode public flight path: %v", err)
	}
	if len(path) != 1 {
		t.Fatalf("public path point count = %d, want 1", len(path))
	}
	if _, exists := path[0]["session_id"]; exists {
		t.Fatal("public path unexpectedly contains a session id")
	}
}

func TestListPublicLocationsAllowsAnonymousRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &trackingHandlerTestService{}
	engine := gin.New()
	engine.GET("/map/active-locations", NewTrackingHandler(service).ListPublicLocations)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/map/active-locations?west=8&south=47&east=9&north=48", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if service.userID != uuid.Nil {
		t.Fatalf("anonymous requester id = %s, want nil UUID", service.userID)
	}
}

func TestListPublicLocationsRejectsInvalidBoundsAndCursor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	service := &trackingHandlerTestService{}
	engine.GET("/map/active-locations", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, uuid.New())
		NewTrackingHandler(service).ListPublicLocations(c)
	})
	for _, target := range []string{
		"/map/active-locations?west=0&south=20&east=1",
		"/map/active-locations?west=0&south=0&east=1&north=1&cursor=not-a-uuid",
	} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("GET %s status = %d, want %d", target, response.Code, http.StatusBadRequest)
		}
	}
}

func TestSetPublicSharingAndStartSessionHonorConsent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &trackingHandlerTestService{}
	userID := uuid.New()
	engine := gin.New()
	engine.POST("/tracking/sessions", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		NewTrackingHandler(service).Start(c)
	})
	engine.PATCH("/tracking/sessions/:session_id/public-sharing", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		NewTrackingHandler(service).SetPublicSharing(c)
	})
	startedAt := time.Now().UTC().Truncate(time.Second)
	body, _ := json.Marshal(map[string]any{
		"pilot_name":     "pilot_one",
		"start_time":     startedAt,
		"publicly_share": true,
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/tracking/sessions", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !service.publiclyShare || service.userID != userID {
		t.Fatalf("start status=%d consent=%v user=%s body=%s", response.Code, service.publiclyShare, service.userID, response.Body.String())
	}

	sessionID := uuid.New()
	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPatch, "/tracking/sessions/"+sessionID.String()+"/public-sharing", strings.NewReader(`{"publicly_share":false}`))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || service.publiclyShare || service.userID != userID || service.sessionID != sessionID {
		t.Fatalf("sharing status=%d consent=%v user=%s session=%s", response.Code, service.publiclyShare, service.userID, service.sessionID)
	}
}
