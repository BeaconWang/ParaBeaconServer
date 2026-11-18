package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/middleware"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/repository"
	"github.com/parabeacon/server/internal/service"
	"gorm.io/gorm"
)

type chatStatusTestRepository struct {
	pilotID uuid.UUID
	sharing bool
}

func (r *chatStatusTestRepository) FindChatUserByID(_ context.Context, id uuid.UUID) (*repository.ChatUser, error) {
	if id != r.pilotID {
		return nil, gorm.ErrRecordNotFound
	}
	return &repository.ChatUser{ID: r.pilotID, PilotNumber: 234567, Username: "partner"}, nil
}

func (r *chatStatusTestRepository) FindChatUserByPilotNumber(_ context.Context, number int) (*repository.ChatUser, error) {
	if number != 234567 {
		return nil, gorm.ErrRecordNotFound
	}
	return &repository.ChatUser{ID: r.pilotID, PilotNumber: number, Username: "partner"}, nil
}

func (r *chatStatusTestRepository) HasFreshPublicLocation(_ context.Context, number int, _, _ time.Time) (bool, error) {
	return number == 234567 && r.sharing, nil
}

func (*chatStatusTestRepository) CreateChatMessage(context.Context, *model.ChatMessage) error {
	return nil
}

func (*chatStatusTestRepository) ListPublicChat(context.Context, time.Time, uuid.UUID, int) ([]model.ChatMessage, error) {
	return nil, nil
}

func (*chatStatusTestRepository) ListChannelChat(context.Context, int, time.Time, uuid.UUID, int) ([]model.ChatMessage, error) {
	return nil, nil
}

func (*chatStatusTestRepository) ListDirectChat(context.Context, uuid.UUID, uuid.UUID, time.Time, uuid.UUID, int) ([]model.ChatMessage, error) {
	return nil, nil
}

func TestPilotLocationStatusReturnsOnlyBooleanForAuthenticatedChatUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requesterID := uuid.New()
	partnerID := uuid.New()
	repository := &chatStatusTestRepository{pilotID: partnerID, sharing: true}
	handler := NewChatHandler(service.NewChatService(repository), nil)
	engine := gin.New()
	engine.GET("/chat/pilots/:pilot_number/location-status", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, requesterID)
		handler.PilotLocationStatus(c)
	})

	request := httptest.NewRequest(http.MethodGet, "/chat/pilots/234567/location-status", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	var sharing bool
	if err := json.Unmarshal(body["actively_sharing_location"], &sharing); err != nil || !sharing {
		t.Fatalf("sharing = %v, err=%v; want true", sharing, err)
	}
	if len(body) != 1 {
		t.Fatalf("location-status exposed extra fields: %v", body)
	}

	request = httptest.NewRequest(http.MethodGet, "/chat/pilots/345678/location-status", nil)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unknown pilot response = %d %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode unknown pilot status: %v", err)
	}
	if err := json.Unmarshal(body["actively_sharing_location"], &sharing); err != nil || sharing {
		t.Fatalf("unknown pilot sharing = %v, err=%v; want false", sharing, err)
	}
	if len(body) != 1 {
		t.Fatalf("unknown pilot response exposed extra fields: %v", body)
	}
}

func TestPilotLocationStatusRequiresAuthenticationAndValidPilotNumber(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requesterID := uuid.New()
	repository := &chatStatusTestRepository{pilotID: uuid.New(), sharing: true}
	handler := NewChatHandler(service.NewChatService(repository), nil)
	engine := gin.New()
	engine.GET("/chat/pilots/:pilot_number/location-status", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, requesterID)
		handler.PilotLocationStatus(c)
	})

	request := httptest.NewRequest(http.MethodGet, "/chat/pilots/123/location-status", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid pilot number status = %d, want %d", response.Code, http.StatusBadRequest)
	}

	anonymousEngine := gin.New()
	anonymousEngine.GET("/chat/pilots/:pilot_number/location-status", handler.PilotLocationStatus)
	request = httptest.NewRequest(http.MethodGet, "/chat/pilots/234567/location-status", nil)
	response = httptest.NewRecorder()
	anonymousEngine.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}
