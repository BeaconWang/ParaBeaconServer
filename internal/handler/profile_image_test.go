package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/middleware"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/service"
	"gorm.io/gorm"
)

type profileImageHandlerUsers struct {
	user *model.User
}

func (r *profileImageHandlerUsers) FindByID(_ context.Context, id uuid.UUID) (*model.User, error) {
	if r.user == nil || r.user.ID != id {
		return nil, gorm.ErrRecordNotFound
	}
	copy := *r.user
	return &copy, nil
}

func (r *profileImageHandlerUsers) UpdateProfileImageKey(_ context.Context, id uuid.UUID, key string) (string, error) {
	if r.user == nil || r.user.ID != id {
		return "", gorm.ErrRecordNotFound
	}
	old := r.user.ProfileImageKey
	r.user.ProfileImageKey = key
	return old, nil
}

func profileImagePNG(t *testing.T) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 3, 2))
	picture.Set(0, 0, color.RGBA{R: 220, A: 255})
	var output bytes.Buffer
	if err := png.Encode(&output, picture); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return output.Bytes()
}

func profileImageUploadRequest(t *testing.T, uri string, imageBytes []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", "pilot.png")
	if err != nil {
		t.Fatalf("create upload part: %v", err)
	}
	if _, err := part.Write(imageBytes); err != nil {
		t.Fatalf("write image part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, uri, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func TestProfileImageUploadServeAndRemove(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	users := &profileImageHandlerUsers{user: &model.User{
		ID: userID, Username: "pilot_one", DisplayName: "Pilot One",
		PilotNumber: intPointer(123456),
	}}
	storage, err := service.NewProfileImageStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewProfileImageStorage() error = %v", err)
	}
	handler := NewProfileImageHandler(service.NewProfileImageService(users, storage))
	engine := gin.New()
	engine.POST("/api/v1/user/profile-image", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		handler.Upload(c)
	})
	engine.DELETE("/api/v1/user/profile-image", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, userID)
		handler.Remove(c)
	})
	engine.GET("/api/v1/profile-images/:image_key", handler.Get)

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, profileImageUploadRequest(t, "/api/v1/user/profile-image", profileImagePNG(t)))
	if response.Code != http.StatusOK {
		t.Fatalf("upload status = %d: %s", response.Code, response.Body.String())
	}
	var uploaded struct {
		User struct {
			ProfileImageURL string `json:"profile_image_url"`
		} `json:"user"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &uploaded); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if uploaded.User.ProfileImageURL == "" || !strings.HasPrefix(uploaded.User.ProfileImageURL, "/api/v1/profile-images/") {
		t.Fatalf("profile image URL = %q", uploaded.User.ProfileImageURL)
	}

	imageResponse := httptest.NewRecorder()
	engine.ServeHTTP(imageResponse, httptest.NewRequest(http.MethodGet, uploaded.User.ProfileImageURL, nil))
	if imageResponse.Code != http.StatusOK || imageResponse.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("image GET = %d, content-type=%q", imageResponse.Code, imageResponse.Header().Get("Content-Type"))
	}
	if imageResponse.Header().Get("X-Content-Type-Options") != "nosniff" || imageResponse.Header().Get("ETag") == "" {
		t.Fatalf("image security/cache headers missing: %v", imageResponse.Header())
	}

	request := httptest.NewRequest(http.MethodDelete, "/api/v1/user/profile-image", nil)
	removeResponse := httptest.NewRecorder()
	engine.ServeHTTP(removeResponse, request)
	if removeResponse.Code != http.StatusOK || users.user.ProfileImageKey != "" {
		t.Fatalf("remove status = %d, key=%q: %s", removeResponse.Code, users.user.ProfileImageKey, removeResponse.Body.String())
	}
	missingResponse := httptest.NewRecorder()
	engine.ServeHTTP(missingResponse, httptest.NewRequest(http.MethodGet, uploaded.User.ProfileImageURL, nil))
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("removed image status = %d, want not found", missingResponse.Code)
	}
}

func intPointer(value int) *int { return &value }
