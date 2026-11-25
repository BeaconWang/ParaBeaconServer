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

type testAccountLookup struct {
	account *model.User
}

func (l testAccountLookup) FindByID(_ context.Context, id uuid.UUID) (*model.User, error) {
	if l.account != nil && l.account.ID == id {
		return l.account, nil
	}
	return nil, nil
}

func TestRequireBeaconAccountAllowsBeaconCaseInsensitively(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	tokens := utils.NewTokenManager("test-jwt-secret-with-at-least-32-bytes", "test-issuer", time.Hour)
	token, _, err := tokens.Issue(userID)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	engine := gin.New()
	engine.POST("/admin/releases", RequireBeaconAccount(tokens, testAccountLookup{account: &model.User{ID: userID, Username: "BeAcOn"}}), func(c *gin.Context) {
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

func TestRequireBeaconAccountRejectsOtherAccounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	tokens := utils.NewTokenManager("test-jwt-secret-with-at-least-32-bytes", "test-issuer", time.Hour)
	token, _, err := tokens.Issue(userID)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	engine := gin.New()
	engine.POST("/admin/releases", RequireBeaconAccount(tokens, testAccountLookup{account: &model.User{ID: userID, Username: "pilot"}}), func(c *gin.Context) {
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

func TestRequireBeaconAccountProvidesAccessToAllReleaseOperations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	tokens := utils.NewTokenManager("test-jwt-secret-with-at-least-32-bytes", "test-issuer", time.Hour)
	token, _, err := tokens.Issue(userID)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	middleware := RequireBeaconAccount(tokens, testAccountLookup{account: &model.User{ID: userID, Username: "BEACON"}})
	engine := gin.New()
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		engine.Handle(method, "/admin/releases", middleware, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		request := httptest.NewRequest(method, "/admin/releases", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Errorf("%s status = %d, want %d", method, response.Code, http.StatusNoContent)
		}
	}
}

func TestRequireBeaconAccountRejectsMissingAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tokens := utils.NewTokenManager("test-jwt-secret-with-at-least-32-bytes", "test-issuer", time.Hour)
	engine := gin.New()
	engine.POST("/admin/releases", RequireBeaconAccount(tokens, testAccountLookup{}), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/releases", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}
