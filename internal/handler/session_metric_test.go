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
	"github.com/parabeacon/server/internal/model"
)

type sessionMetricHandlerTestIngestor struct {
	event *model.SessionMetricEvent
	err   error
}

func (f *sessionMetricHandlerTestIngestor) Ingest(_ context.Context, event *model.SessionMetricEvent) error {
	copy := *event
	f.event = &copy
	return f.err
}

func TestSessionMetricsHandlerIngestUsesPeerAddressAndAuthenticatedIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ingestor := &sessionMetricHandlerTestIngestor{}
	userID := uuid.New()
	engine := gin.New()
	if err := engine.SetTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}
	engine.POST("/metrics", func(c *gin.Context) {
		c.Set("authenticated_user_id", userID)
		NewSessionMetricsHandler(ingestor, nil, nil).Ingest(c)
	})

	payload := validSessionMetricPayload(userID.String())
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/metrics", strings.NewReader(string(body)))
	request.RemoteAddr = "192.0.2.10:4000"
	request.Header.Set("X-Forwarded-For", "198.51.100.77")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d: %s", http.StatusAccepted, response.Code, response.Body.String())
	}
	if ingestor.event == nil {
		t.Fatal("expected metric to be ingested")
	}
	if ingestor.event.ClientIP != "192.0.2.10" {
		t.Fatalf("expected direct peer IP, got %q", ingestor.event.ClientIP)
	}
	if ingestor.event.UserID == nil || *ingestor.event.UserID != userID {
		t.Fatalf("expected authenticated user identity, got %v", ingestor.event.UserID)
	}
}

func TestSessionMetricsHandlerDoesNotTrustAnonymousUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ingestor := &sessionMetricHandlerTestIngestor{}
	engine := gin.New()
	if err := engine.SetTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}
	engine.POST("/metrics", NewSessionMetricsHandler(ingestor, nil, nil).Ingest)

	body, err := json.Marshal(validSessionMetricPayload(uuid.New().String()))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/metrics", strings.NewReader(string(body)))
	request.RemoteAddr = "192.0.2.11:4000"
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d: %s", http.StatusAccepted, response.Code, response.Body.String())
	}
	if ingestor.event.UserID != nil {
		t.Fatalf("expected anonymous metric to have no user id, got %v", ingestor.event.UserID)
	}
}

func TestSessionMetricsHandlerRejectsOversizedPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/metrics", NewSessionMetricsHandler(&sessionMetricHandlerTestIngestor{}, nil, nil).Ingest)
	request := httptest.NewRequest(http.MethodPost, "/metrics", strings.NewReader(`{"diagnostics":"`+strings.Repeat("x", maxSessionMetricRequestBytes)+`"}`))
	request.RemoteAddr = "192.0.2.12:4000"
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected %d, got %d", http.StatusRequestEntityTooLarge, response.Code)
	}
}

func validSessionMetricPayload(userID string) map[string]interface{} {
	return map[string]interface{}{
		"event_id":          uuid.NewString(),
		"session_id":        uuid.NewString(),
		"hardware_id":       uuid.NewString(),
		"app_version":       "1.2.3+45",
		"user_id":           userID,
		"run_time_sec":      0,
		"total_runtime_sec": 0,
		"client_timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
		"diagnostics":       map[string]string{"os": "test"},
	}
}
