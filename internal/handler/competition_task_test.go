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

type competitionTaskHandlerTestService struct {
	userID    uuid.UUID
	task      json.RawMessage
	updatedAt time.Time
	tasks     []model.CompetitionTask
}

func (s *competitionTaskHandlerTestService) Replace(_ context.Context, userID uuid.UUID, task json.RawMessage) (time.Time, error) {
	s.userID, s.task = userID, task
	return s.updatedAt, nil
}

func (s *competitionTaskHandlerTestService) ListAll(context.Context) ([]model.CompetitionTask, error) {
	return s.tasks, nil
}

func TestCompetitionTaskReplaceUsesAuthenticatedAccountAndListHidesOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	service := &competitionTaskHandlerTestService{updatedAt: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)}
	service.tasks = []model.CompetitionTask{{UserID: userID, TaskData: `{"taskType":"CLASSIC","version":1,"turnpoints":[{"radius":400,"waypoint":{"lat":47.1,"lon":8.5}}]}`, UpdatedAt: service.updatedAt}}
	engine := gin.New()
	engine.PUT("/user/competition-task", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		NewCompetitionTaskHandler(service).Replace(c)
	})
	engine.GET("/competition-tasks", NewCompetitionTaskHandler(service).List)

	body := `{"task":{"taskType":"CLASSIC","version":1,"turnpoints":[{"radius":400,"waypoint":{"lat":47.1,"lon":8.5}}]}}`
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/user/competition-task", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.userID != userID || len(service.task) == 0 {
		t.Fatalf("replace status=%d user=%s task=%s body=%s", response.Code, service.userID, service.task, response.Body.String())
	}

	response = httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/competition-tasks", nil))
	var payload struct {
		Tasks []map[string]json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(payload.Tasks) != 1 || len(payload.Tasks[0]["task"]) == 0 {
		t.Fatalf("list status=%d payload=%s", response.Code, response.Body.String())
	}
	if _, exposed := payload.Tasks[0]["user_id"]; exposed {
		t.Fatal("server list exposed the task owner's ID")
	}
}

func TestCompetitionTaskReplaceRejectsUnknownAndOversizedRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &competitionTaskHandlerTestService{}
	engine := gin.New()
	engine.PUT("/user/competition-task", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, uuid.New())
		NewCompetitionTaskHandler(service).Replace(c)
	})
	for _, body := range []string{
		`{"task":{},"user_id":"spoofed"}`,
		`{"task":` + strings.Repeat(" ", maxCompetitionTaskRequestBytes) + `}`,
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/user/competition-task", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", response.Code, http.StatusBadRequest)
		}
	}
}
