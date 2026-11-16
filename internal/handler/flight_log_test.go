package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/middleware"
	"github.com/parabeacon/server/internal/model"
)

type flightLogHandlerTestService struct {
	userID        uuid.UUID
	after         uuid.UUID
	flights       []json.RawMessage
	syncCalls     int
	syncID        uuid.UUID
	expectedCount int
	batchIndex    int
	batchCalls    int
	commitCalls   int
	beginCalls    int
}

func (s *flightLogHandlerTestService) Import(_ context.Context, userID uuid.UUID, flights []json.RawMessage) (int, int, error) {
	s.userID, s.flights = userID, flights
	return len(flights), 0, nil
}

func (s *flightLogHandlerTestService) Sync(_ context.Context, userID uuid.UUID, flights []json.RawMessage) (int, error) {
	s.userID, s.flights = userID, flights
	s.syncCalls++
	return len(flights), nil
}

func (s *flightLogHandlerTestService) BeginSync(_ context.Context, userID uuid.UUID, expectedCount int) (uuid.UUID, error) {
	s.userID, s.expectedCount, s.syncID = userID, expectedCount, uuid.New()
	s.beginCalls++
	return s.syncID, nil
}

func (s *flightLogHandlerTestService) UploadSyncBatch(_ context.Context, userID, syncID uuid.UUID, batchIndex int, flights []json.RawMessage) error {
	s.userID, s.syncID, s.batchIndex, s.flights = userID, syncID, batchIndex, flights
	s.batchCalls++
	return nil
}

func (s *flightLogHandlerTestService) CommitSync(_ context.Context, userID, syncID uuid.UUID) (int, error) {
	s.userID, s.syncID = userID, syncID
	s.commitCalls++
	return s.expectedCount, nil
}

func (s *flightLogHandlerTestService) List(_ context.Context, userID, after uuid.UUID) ([]model.FlightLog, bool, error) {
	s.userID, s.after = userID, after
	return []model.FlightLog{{ID: uuid.New(), Data: `{"startTime":1,"endTime":2,"pointCount":1}`}}, false, nil
}

func TestFlightLogImportAndListUseAuthenticatedUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &flightLogHandlerTestService{}
	userID := uuid.New()
	engine := gin.New()
	engine.POST("/flight-logs", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		NewFlightLogHandler(service).Import(c)
	})
	engine.GET("/flight-logs", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		NewFlightLogHandler(service).List(c)
	})
	engine.PUT("/flight-logs/sync", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		NewFlightLogHandler(service).Sync(c)
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/flight-logs", strings.NewReader(`{"flights":[{"startTime":1700000000000,"endTime":1700000001000,"pointCount":1}]}`))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.userID != userID || len(service.flights) != 1 {
		t.Fatalf("import status=%d user=%s flights=%d body=%s", response.Code, service.userID, len(service.flights), response.Body.String())
	}

	cursor := uuid.New()
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/flight-logs?cursor="+cursor.String(), nil))
	if response.Code != http.StatusOK || service.userID != userID || service.after != cursor {
		t.Fatalf("list status=%d user=%s cursor=%s body=%s", response.Code, service.userID, service.after, response.Body.String())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(payload["flights"], &entries); err != nil || len(entries) != 1 {
		t.Fatalf("flight entries = %s, err=%v", payload["flights"], err)
	}
	if string(entries[0]["flight"]) != `{"startTime":1,"endTime":2,"pointCount":1}` {
		t.Fatalf("flight payload was not returned as JSON: %s", entries[0]["flight"])
	}
}

func TestFlightLogStagedSyncEndpointsUseAuthenticatedAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &flightLogHandlerTestService{}
	userID := uuid.New()
	engine := gin.New()
	handler := NewFlightLogHandler(service)
	engine.POST("/flight-logs/syncs", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		handler.BeginSync(c)
	})
	engine.PUT("/flight-logs/syncs/:sync_id/batches/:batch_index", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		handler.UploadSyncBatch(c)
	})
	engine.POST("/flight-logs/syncs/:sync_id/commit", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		handler.CommitSync(c)
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/flight-logs/syncs", strings.NewReader(`{"expected_count":1}`))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || service.userID != userID || service.expectedCount != 1 {
		t.Fatalf("begin status=%d user=%s count=%d body=%s", response.Code, service.userID, service.expectedCount, response.Body.String())
	}

	flight := `{"startTime":1700000000000,"endTime":1700000001000,"pointCount":1}`
	batchPath := "/flight-logs/syncs/" + service.syncID.String() + "/batches/0"
	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPut, batchPath, strings.NewReader(`{"flights":[`+flight+`]}`))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.batchCalls != 1 || service.batchIndex != 0 || service.userID != userID {
		t.Fatalf("batch status=%d user=%s calls=%d index=%d body=%s", response.Code, service.userID, service.batchCalls, service.batchIndex, response.Body.String())
	}

	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/flight-logs/syncs/"+service.syncID.String()+"/commit", nil)
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.commitCalls != 1 || service.userID != userID {
		t.Fatalf("commit status=%d user=%s calls=%d body=%s", response.Code, service.userID, service.commitCalls, response.Body.String())
	}
}

func TestFlightLogSyncAcceptsEmptySnapshotAndRejectsNull(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &flightLogHandlerTestService{}
	userID := uuid.New()
	engine := gin.New()
	engine.PUT("/flight-logs/sync", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		NewFlightLogHandler(service).Sync(c)
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/flight-logs/sync", strings.NewReader(`{"flights":[]}`))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.userID != userID || service.syncCalls != 1 || service.flights == nil || len(service.flights) != 0 {
		t.Fatalf("empty sync status=%d user=%s calls=%d flights=%v body=%s", response.Code, service.userID, service.syncCalls, service.flights, response.Body.String())
	}

	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPut, "/flight-logs/sync", strings.NewReader(`{"flights":null}`))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || service.syncCalls != 1 {
		t.Fatalf("null snapshot status=%d calls=%d; body=%s", response.Code, service.syncCalls, response.Body.String())
	}
}

func TestFlightLogImportRejectsOversizedAndInvalidBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	service := &flightLogHandlerTestService{}
	engine.POST("/flight-logs", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, uuid.New())
		NewFlightLogHandler(service).Import(c)
	})
	for _, body := range []string{`{"flights":[],"unexpected":true}`, `{"flights":[` + strings.Repeat(" ", maxFlightLogRequestBytes) + `]}`} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/flight-logs", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", response.Code, http.StatusBadRequest)
		}
	}
}
