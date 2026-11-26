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
	servicepkg "github.com/parabeacon/server/internal/service"
)

type competitionTaskHandlerTestService struct {
	userID           uuid.UUID
	task             json.RawMessage
	updatedAt        time.Time
	tasks            []model.CompetitionTaskListEntry
	currentTaskOwner *uuid.UUID
	deletedUserID    uuid.UUID
	deleteErr        error
}

func (s *competitionTaskHandlerTestService) Replace(_ context.Context, userID uuid.UUID, task json.RawMessage) (time.Time, error) {
	s.userID, s.task = userID, task
	return s.updatedAt, nil
}

func (s *competitionTaskHandlerTestService) DeleteOwnTask(_ context.Context, userID uuid.UUID) error {
	s.deletedUserID = userID
	return s.deleteErr
}

func (s *competitionTaskHandlerTestService) ListAll(context.Context) ([]model.CompetitionTaskListEntry, error) {
	return s.tasks, nil
}

func (s *competitionTaskHandlerTestService) CurrentTaskOwner(context.Context, uuid.UUID) (*uuid.UUID, error) {
	return s.currentTaskOwner, nil
}

func (s *competitionTaskHandlerTestService) SetCurrentTaskOwner(_ context.Context, _ uuid.UUID, ownerID *uuid.UUID) error {
	s.currentTaskOwner = ownerID
	return nil
}

func TestCompetitionTaskReplaceUsesAuthenticatedAccountAndListHidesOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	service := &competitionTaskHandlerTestService{updatedAt: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC), currentTaskOwner: &userID}
	service.tasks = []model.CompetitionTaskListEntry{{
		UserID:      userID,
		TaskData:    `{"taskType":"CLASSIC","version":1,"turnpoints":[{"radius":400,"waypoint":{"lat":47.1,"lon":8.5}}]}`,
		UpdatedAt:   service.updatedAt,
		Username:    "pilot-one",
		DisplayName: "Pilot One",
	}}
	engine := gin.New()
	engine.PUT("/user/competition-task", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		NewCompetitionTaskHandler(service).Replace(c)
	})
	engine.GET("/competition-tasks", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		NewCompetitionTaskHandler(service).List(c)
	})
	engine.PUT("/user/current-competition-task", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		NewCompetitionTaskHandler(service).SetCurrent(c)
	})

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
	if string(payload.Tasks[0]["username"]) != `"pilot-one"` ||
		string(payload.Tasks[0]["display_name"]) != `"Pilot One"` ||
		string(payload.Tasks[0]["owner_id"]) != `"`+userID.String()+`"` {
		t.Fatalf("server list owner fields = %s / %s / %s", payload.Tasks[0]["username"], payload.Tasks[0]["display_name"], payload.Tasks[0]["owner_id"])
	}
	var responsePayload map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &responsePayload); err != nil {
		t.Fatal(err)
	}
	if string(responsePayload["current_task_owner_id"]) != `"`+userID.String()+`"` {
		t.Fatalf("current_task_owner_id = %s", responsePayload["current_task_owner_id"])
	}
}

func TestCompetitionTaskDeleteOwnRequiresAuthenticationAndRemovesOnlyOwnTask(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	service := &competitionTaskHandlerTestService{}
	handler := NewCompetitionTaskHandler(service)
	authenticated := gin.New()
	authenticated.DELETE("/user/competition-task", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		handler.DeleteOwn(c)
	})

	response := httptest.NewRecorder()
	authenticated.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/user/competition-task", nil))
	if response.Code != http.StatusNoContent || service.deletedUserID != userID {
		t.Fatalf("delete status=%d user=%s body=%s", response.Code, service.deletedUserID, response.Body.String())
	}

	service.deleteErr = servicepkg.ErrCompetitionTaskNotFound
	response = httptest.NewRecorder()
	authenticated.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/user/competition-task", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing task status=%d body=%s", response.Code, response.Body.String())
	}

	unauthenticated := gin.New()
	unauthenticated.DELETE("/user/competition-task", handler.DeleteOwn)
	response = httptest.NewRecorder()
	unauthenticated.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/user/competition-task", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated delete status=%d", response.Code)
	}
}

func TestCompetitionTaskCurrentReturnsOnlyAuthenticatedSelection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID, ownerID := uuid.New(), uuid.New()
	service := &competitionTaskHandlerTestService{currentTaskOwner: &ownerID}
	handler := NewCompetitionTaskHandler(service)
	engine := gin.New()
	engine.GET("/user/current-competition-task", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		handler.Current(c)
	})

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/user/current-competition-task", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), ownerID.String()) {
		t.Fatalf("current status=%d body=%s", response.Code, response.Body.String())
	}

	service.currentTaskOwner = nil
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/user/current-competition-task", nil))
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != "{\"current_task_owner_id\":null}" {
		t.Fatalf("cleared current status=%d body=%s", response.Code, response.Body.String())
	}

	unauthenticated := gin.New()
	unauthenticated.GET("/user/current-competition-task", handler.Current)
	response = httptest.NewRecorder()
	unauthenticated.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/user/current-competition-task", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated current status=%d", response.Code)
	}
}

func TestCompetitionTaskSetCurrentAllowsSelectionAndUnselection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID, ownerID := uuid.New(), uuid.New()
	service := &competitionTaskHandlerTestService{}
	engine := gin.New()
	engine.PUT("/user/current-competition-task", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		NewCompetitionTaskHandler(service).SetCurrent(c)
	})
	for _, test := range []struct {
		body string
		want *uuid.UUID
	}{
		{body: `{"task_owner_id":"` + ownerID.String() + `"}`, want: &ownerID},
		{body: `{"task_owner_id":null}`, want: nil},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/user/current-competition-task", strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("selection status=%d body=%s", response.Code, response.Body.String())
		}
		if (test.want == nil) != (service.currentTaskOwner == nil) ||
			(test.want != nil && *service.currentTaskOwner != *test.want) {
			t.Fatalf("stored selection=%v, want %v", service.currentTaskOwner, test.want)
		}
	}
	for _, body := range []string{`{}`, `{"task_owner_id":"not-a-uuid"}`} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/user/current-competition-task", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("body %s status=%d, want %d", body, response.Code, http.StatusBadRequest)
		}
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
