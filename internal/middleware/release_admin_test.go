package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/pkg/utils"
)

func TestRequireReleaseAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := "test-release-admin-token-with-strong-random-value"
	engine := gin.New()
	engine.POST("/admin/releases", RequireReleaseAdmin(secret), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	for _, test := range []struct {
		name   string
		token  string
		status int
	}{
		{name: "missing", status: http.StatusUnauthorized},
		{name: "wrong", token: "wrong-token", status: http.StatusUnauthorized},
		{name: "valid", token: secret, status: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/admin/releases", nil)
			if test.token != "" {
				request.Header.Set("X-Release-Admin-Token", test.token)
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}

func TestRequireReleaseAdminFailsClosedWhenUnset(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/admin/releases", RequireReleaseAdmin(""), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/releases", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

type testAccountLookup struct {
	account *model.User
}

func (l testAccountLookup) FindByID(_ context.Context, id uuid.UUID) (*model.User, error) {
	if l.account != nil && l.account.ID == id {
		return l.account, nil
	}
	return nil, nil
}

func TestRequireReleaseAdminOrBeaconAccountAllowsBeaconCaseInsensitively(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	tokens := utils.NewTokenManager("test-jwt-secret-with-at-least-32-bytes", "test-issuer", time.Hour)
	token, _, err := tokens.Issue(userID)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	engine := gin.New()
	engine.POST("/admin/releases", RequireReleaseAdminOrBeaconAccount("", tokens, testAccountLookup{account: &model.User{ID: userID, Username: "BeAcOn"}}), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/admin/releases", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestRequireReleaseAdminOrBeaconAccountRejectsOtherAccounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	tokens := utils.NewTokenManager("test-jwt-secret-with-at-least-32-bytes", "test-issuer", time.Hour)
	token, _, err := tokens.Issue(userID)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	engine := gin.New()
	engine.POST("/admin/releases", RequireReleaseAdminOrBeaconAccount("", tokens, testAccountLookup{account: &model.User{ID: userID, Username: "pilot"}}), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/admin/releases", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
}
