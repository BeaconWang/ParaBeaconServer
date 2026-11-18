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
	"github.com/parabeacon/server/internal/service"
)

type profileTestUsers struct {
	user *model.User
}

func (r *profileTestUsers) Create(_ context.Context, user *model.User) error {
	r.user = user
	return nil
}
func (*profileTestUsers) FindByLogin(context.Context, string) (*model.User, error) {
	return nil, nil
}
func (r *profileTestUsers) FindByID(_ context.Context, id uuid.UUID) (*model.User, error) {
	if r.user == nil || r.user.ID != id {
		return nil, service.ErrNotFound
	}
	copy := *r.user
	return &copy, nil
}
func (r *profileTestUsers) UpdateDisplayName(_ context.Context, id uuid.UUID, displayName string) error {
	if r.user == nil || r.user.ID != id {
		return service.ErrNotFound
	}
	r.user.DisplayName = displayName
	return nil
}

func TestUpdateProfileDisplayNameRequiresAuthenticatedOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	users := &profileTestUsers{user: &model.User{
		ID: userID, Username: "pilot_one", DisplayName: "Old Name",
	}}
	handler := NewUserHandler(service.NewAuthService(users, nil))
	engine := gin.New()
	engine.PATCH("/profile", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		handler.UpdateProfile(c)
	})

	request := httptest.NewRequest(http.MethodPatch, "/profile", strings.NewReader(`{"display_name":"New Pilot"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if users.user.DisplayName != "New Pilot" {
		t.Fatalf("stored display name = %q", users.user.DisplayName)
	}
	var responseBody struct {
		User struct {
			DisplayName string `json:"display_name"`
		} `json:"user"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &responseBody); err != nil {
		t.Fatalf("decode profile response: %v", err)
	}
	if responseBody.User.DisplayName != "New Pilot" {
		t.Fatalf("response display name = %q", responseBody.User.DisplayName)
	}
}

func TestUpdateProfileRejectsInvalidDisplayNames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	users := &profileTestUsers{user: &model.User{ID: userID, Username: "pilot_one"}}
	handler := NewUserHandler(service.NewAuthService(users, nil))
	for _, body := range []string{
		`{"display_name":"` + strings.Repeat("A", 49) + `"}`,
		"{\"display_name\":\"line\\nbreak\"}",
	} {
		engine := gin.New()
		engine.PATCH("/profile", func(c *gin.Context) {
			c.Set(middleware.AuthenticatedUserIDKey, userID)
			handler.UpdateProfile(c)
		})
		request := httptest.NewRequest(http.MethodPatch, "/profile", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("body %q status = %d, want %d", body, response.Code, http.StatusBadRequest)
		}
	}
}
