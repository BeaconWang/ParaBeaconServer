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
	"github.com/parabeacon/server/internal/repository"
	"github.com/parabeacon/server/internal/service"
	"github.com/parabeacon/server/pkg/utils"
	"gorm.io/gorm"
)

type authTestUsers struct {
	user *model.User
}

func (r *authTestUsers) Create(_ context.Context, user *model.User) error {
	if user.ID == uuid.Nil {
		user.ID = uuid.New()
	}
	if user.PilotNumber == nil {
		pilotNumber := 123456
		user.PilotNumber = &pilotNumber
	}
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now().UTC()
	}
	r.user = user
	return nil
}

func (r *authTestUsers) FindByLogin(_ context.Context, identifier string) (*model.User, error) {
	if (r.user.Email != nil && identifier == *r.user.Email) || identifier == r.user.Username {
		return r.user, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *authTestUsers) FindByID(_ context.Context, id uuid.UUID) (*model.User, error) {
	if r.user.ID == id {
		return r.user, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *authTestUsers) UpdateChannelNumber(_ context.Context, id uuid.UUID, channelNumber int) error {
	if r.user.ID != id {
		return gorm.ErrRecordNotFound
	}
	r.user.ChannelNumber = channelNumber
	return nil
}

func (r *authTestUsers) UpdateDisplayName(_ context.Context, id uuid.UUID, displayName string) error {
	if r.user.ID != id {
		return gorm.ErrRecordNotFound
	}
	r.user.DisplayName = displayName
	return nil
}

var _ repository.UserRepository = (*authTestUsers)(nil)

func TestLoginAcceptsUsernameAndLegacyEmail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	password := "a-long-unique-passphrase"
	email := "pilot@example.com"
	hash, err := utils.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	users := &authTestUsers{user: &model.User{
		ID:           uuid.New(),
		Username:     "sky_rider",
		DisplayName:  "Sky Rider",
		Email:        &email,
		PasswordHash: hash,
		CreatedAt:    time.Now().UTC(),
	}}
	tokens := utils.NewTokenManager("0123456789abcdef0123456789abcdef", "test-issuer", time.Minute)
	handler := NewAuthHandler(service.NewAuthService(users, tokens))
	engine := gin.New()
	engine.POST("/login", handler.Login)

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "username identifier", body: `{"identifier":"SKY_RIDER","password":"` + password + `"}`},
		{name: "legacy email field", body: `{"email":"PILOT@example.com","password":"` + password + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			engine.ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body = %s", res.Code, http.StatusOK, res.Body.String())
			}
			var response struct {
				AccessToken string `json:"access_token"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.AccessToken == "" {
				t.Fatal("login response has no access token")
			}
		})
	}
}

func TestLoginRejectsMultipleIdentifiers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	users := &authTestUsers{user: &model.User{}}
	tokens := utils.NewTokenManager("0123456789abcdef0123456789abcdef", "test-issuer", time.Minute)
	engine := gin.New()
	engine.POST("/login", NewAuthHandler(service.NewAuthService(users, tokens)).Login)
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"identifier":"sky_rider","email":"pilot@example.com","password":"a-long-unique-passphrase"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	engine.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
}

func TestRegisterRequiresOnlyUsernameAndPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	users := &authTestUsers{}
	tokens := utils.NewTokenManager("0123456789abcdef0123456789abcdef", "test-issuer", time.Minute)
	engine := gin.New()
	engine.POST("/register", NewAuthHandler(service.NewAuthService(users, tokens)).Register)
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(`{"username":"sky_rider","password":"abc123"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	engine.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", res.Code, http.StatusCreated, res.Body.String())
	}
	if users.user == nil || users.user.Email != nil {
		t.Fatalf("registered user = %#v, want user with no email", users.user)
	}
	var response struct {
		User map[string]json.RawMessage `json:"user"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := response.User["email"]; ok {
		t.Fatal("registration response unexpectedly includes an absent email")
	}
	var pilotNumber int
	if err := json.Unmarshal(response.User["pilot_number"], &pilotNumber); err != nil {
		t.Fatalf("decode pilot number: %v", err)
	}
	if pilotNumber != 123456 {
		t.Fatalf("pilot number = %d, want 123456", pilotNumber)
	}
	var channelNumber int
	if err := json.Unmarshal(response.User["channel_number"], &channelNumber); err != nil {
		t.Fatalf("decode channel number: %v", err)
	}
	if channelNumber != 0 {
		t.Fatalf("new-account channel number = %d, want 0 (unassigned)", channelNumber)
	}
}
