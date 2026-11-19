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
	"github.com/parabeacon/server/internal/repository"
	"github.com/parabeacon/server/internal/service"
	"github.com/parabeacon/server/pkg/utils"
)

type profileTestUsers struct {
	user            *model.User
	sessionsRevoked bool
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
func (r *profileTestUsers) ChangePassword(_ context.Context, id uuid.UUID, expectedHash, newHash string, _ time.Time) error {
	if r.user == nil || r.user.ID != id {
		return service.ErrNotFound
	}
	if r.user.PasswordHash != expectedHash {
		return repository.ErrPasswordChanged
	}
	r.user.PasswordHash = newHash
	r.sessionsRevoked = true
	return nil
}
func (r *profileTestUsers) UpdateAvatarID(_ context.Context, id uuid.UUID, avatarID int) error {
	if r.user == nil || r.user.ID != id {
		return service.ErrNotFound
	}
	r.user.AvatarID = avatarID
	return nil
}
func (r *profileTestUsers) UpdateChannelNumber(_ context.Context, id uuid.UUID, channelNumber int) error {
	if r.user == nil || r.user.ID != id {
		return service.ErrNotFound
	}
	r.user.ChannelNumber = channelNumber
	return nil
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

func TestUpdateProfileChannelNumber(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	users := &profileTestUsers{user: &model.User{
		ID: userID, Username: "pilot_one", ChannelNumber: 0,
	}}
	handler := NewUserHandler(service.NewAuthService(users, nil))
	engine := gin.New()
	engine.PATCH("/profile", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		handler.UpdateProfile(c)
	})

	request := httptest.NewRequest(http.MethodPatch, "/profile", strings.NewReader(`{"channel_number":456789}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || users.user.ChannelNumber != 456789 {
		t.Fatalf("update channel response = %d %s, stored=%d", response.Code, response.Body.String(), users.user.ChannelNumber)
	}
	var responseBody struct {
		User struct {
			ChannelNumber int `json:"channel_number"`
		} `json:"user"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &responseBody); err != nil {
		t.Fatalf("decode profile response: %v", err)
	}
	if responseBody.User.ChannelNumber != 456789 {
		t.Fatalf("response channel number = %d", responseBody.User.ChannelNumber)
	}

	request = httptest.NewRequest(http.MethodPatch, "/profile", strings.NewReader(`{"channel_number":1000000}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || users.user.ChannelNumber != 456789 {
		t.Fatalf("invalid channel response = %d, stored=%d", response.Code, users.user.ChannelNumber)
	}

	request = httptest.NewRequest(http.MethodPatch, "/profile", strings.NewReader(`{"channel_number":0}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || users.user.ChannelNumber != 0 {
		t.Fatalf("clearing channel response = %d, stored=%d", response.Code, users.user.ChannelNumber)
	}
}

func TestUpdateProfileAvatarID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	users := &profileTestUsers{user: &model.User{ID: userID, Username: "pilot_one"}}
	handler := NewUserHandler(service.NewAuthService(users, nil))
	engine := gin.New()
	engine.PATCH("/profile", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		handler.UpdateProfile(c)
	})

	request := httptest.NewRequest(http.MethodPatch, "/profile", strings.NewReader(`{"avatar_id":5}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || users.user.AvatarID != 5 {
		t.Fatalf("avatar update response = %d %s; stored avatar=%d", response.Code, response.Body.String(), users.user.AvatarID)
	}
	var body struct {
		User struct {
			AvatarID int `json:"avatar_id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode profile response: %v", err)
	}
	if body.User.AvatarID != 5 {
		t.Fatalf("response avatar id = %d, want 5", body.User.AvatarID)
	}

	request = httptest.NewRequest(http.MethodPatch, "/profile", strings.NewReader(`{"avatar_id":999}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || users.user.AvatarID != 5 {
		t.Fatalf("invalid avatar response = %d; stored avatar=%d", response.Code, users.user.AvatarID)
	}
}

func TestChangePasswordRequiresCurrentPasswordAndRevokesSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	oldHash, err := utils.HashPassword("old-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	users := &profileTestUsers{user: &model.User{
		ID: userID, Username: "pilot_one", PasswordHash: oldHash,
	}}
	handler := NewUserHandler(service.NewAuthService(users, nil))
	engine := gin.New()
	engine.POST("/change-password", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		handler.ChangePassword(c)
	})

	request := httptest.NewRequest(http.MethodPost, "/change-password", strings.NewReader(
		`{"current_password":"wrong-pass","new_password":"new-pass-456"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current password status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if users.sessionsRevoked || users.user.PasswordHash != oldHash {
		t.Fatal("wrong current password changed credentials or revoked sessions")
	}

	request = httptest.NewRequest(http.MethodPost, "/change-password", strings.NewReader(
		`{"current_password":"old-pass-123","new_password":"new-pass-456"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("valid password change status = %d, want %d: %s", response.Code, http.StatusNoContent, response.Body.String())
	}
	if !users.sessionsRevoked || utils.VerifyPassword(users.user.PasswordHash, "new-pass-456") != nil {
		t.Fatal("password was not changed and sessions revoked")
	}
	if strings.Contains(response.Body.String(), "new-pass-456") {
		t.Fatal("response exposed the new password")
	}
}

func TestChangePasswordRejectsInvalidNewPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	oldHash, _ := utils.HashPassword("old-pass-123")
	users := &profileTestUsers{user: &model.User{ID: userID, PasswordHash: oldHash}}
	handler := NewUserHandler(service.NewAuthService(users, nil))
	engine := gin.New()
	engine.POST("/change-password", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		handler.ChangePassword(c)
	})
	for _, testCase := range []struct {
		body       string
		wantStatus int
	}{
		{body: `{"current_password":"old-pass-123","new_password":"short"}`, wantStatus: http.StatusBadRequest},
		{body: `{"current_password":"old-pass-123","new_password":"old-pass-123"}`, wantStatus: http.StatusConflict},
		{body: `{"current_password":"old-pass-123","new_password":"` + strings.Repeat("界", 25) + `"}`, wantStatus: http.StatusBadRequest},
	} {
		request := httptest.NewRequest(http.MethodPost, "/change-password", strings.NewReader(testCase.body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != testCase.wantStatus {
			t.Errorf("body %q status = %d, want %d", testCase.body, response.Code, testCase.wantStatus)
		}
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
