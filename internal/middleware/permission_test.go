package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/pkg/utils"
)

type permissionLookupTest struct {
	allowed bool
	err     error
}

func (l permissionLookupTest) HasPermission(context.Context, uuid.UUID, string) (bool, error) {
	return l.allowed, l.err
}

func TestRequirePermissionUsesCurrentServerSidePermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	tokens := utils.NewTokenManager("0123456789abcdef0123456789abcdef", "test-issuer", time.Minute)
	token, _, err := tokens.Issue(userID)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	for _, testCase := range []struct {
		name       string
		lookup     permissionLookupTest
		wantStatus int
	}{
		{name: "allowed", lookup: permissionLookupTest{allowed: true}, wantStatus: http.StatusNoContent},
		{name: "denied", lookup: permissionLookupTest{}, wantStatus: http.StatusForbidden},
		{name: "lookup unavailable", lookup: permissionLookupTest{err: errors.New("database unavailable")}, wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			engine := gin.New()
			engine.GET("/protected", RequirePermission(tokens, testCase.lookup, "apk_management"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, testCase.wantStatus, response.Body.String())
			}
		})
	}
}

func TestRequirePermissionRejectsUnauthenticatedRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/protected", RequirePermission(utils.NewTokenManager("0123456789abcdef0123456789abcdef", "test-issuer", time.Minute), permissionLookupTest{allowed: true}, "xc_route_customization"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/protected", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}
