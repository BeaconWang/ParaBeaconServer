package handler

import (
	"context"
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

type landingPointHandlerTestService struct {
	userID    uuid.UUID
	points    []model.LandingPoint
	defaultID *string
}

func (s *landingPointHandlerTestService) Import(_ context.Context, userID uuid.UUID, points []model.LandingPoint, defaultID *string) error {
	s.userID, s.points, s.defaultID = userID, points, defaultID
	return nil
}

func (s *landingPointHandlerTestService) List(_ context.Context, userID uuid.UUID) ([]model.LandingPoint, *string, error) {
	s.userID = userID
	return s.points, s.defaultID, nil
}

func TestLandingPointImportAndListAreUserScoped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &landingPointHandlerTestService{}
	userID := uuid.New()
	defaultID := "client-1"
	engine := gin.New()
	engine.POST("/landing-points", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		NewLandingPointHandler(service).Import(c)
	})
	engine.GET("/landing-points", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		NewLandingPointHandler(service).List(c)
	})
	body := strings.ReplaceAll(`{"points":[{"id":"client-1","name":"Landing","latitude":47.1,"longitude":8.5,"altitudeM":1200,"createdAt":"`+time.Now().UTC().Format(time.RFC3339Nano)+`","notes":"safe"}],"default_id":"client-1"}`, "client-1", defaultID)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/landing-points", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.userID != userID || len(service.points) != 1 || service.points[0].ClientID != "client-1" {
		t.Fatalf("import status=%d user=%s points=%#v body=%s", response.Code, service.userID, service.points, response.Body.String())
	}
	if service.points[0].UserID != uuid.Nil {
		t.Fatal("request was allowed to select a user id")
	}

	response = httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/landing-points", nil))
	if response.Code != http.StatusOK || service.userID != userID {
		t.Fatalf("list status=%d user=%s body=%s", response.Code, service.userID, response.Body.String())
	}
}

func TestLandingPointImportRejectsUnknownAndOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &landingPointHandlerTestService{}
	engine := gin.New()
	engine.POST("/landing-points", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, uuid.New())
		NewLandingPointHandler(service).Import(c)
	})
	for _, body := range []string{
		`{"points":[],"unexpected":true}`,
		`{"points":[` + strings.Repeat(" ", maxLandingPointRequestBytes) + `]}`,
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/landing-points", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", response.Code, http.StatusBadRequest)
		}
	}
}
