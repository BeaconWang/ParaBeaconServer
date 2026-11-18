package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/pkg/utils"
)

type authSessionValidator struct {
	active    bool
	userID    uuid.UUID
	sessionID uuid.UUID
}

func (v authSessionValidator) IsActiveSession(_ context.Context, sessionID, userID uuid.UUID, _ time.Time) (bool, error) {
	return v.active && sessionID == v.sessionID && userID == v.userID, nil
}

func TestOptionalAuthAllowsAnonymousAndValidatesProvidedCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	sessionID := uuid.New()
	tokens := utils.NewTokenManager("0123456789abcdef0123456789abcdef", "test-issuer", time.Minute)
	accessToken, _, err := tokens.Issue(userID, sessionID)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	for _, testCase := range []struct {
		name       string
		header     string
		wantStatus int
		wantUserID uuid.UUID
	}{
		{name: "anonymous", wantStatus: http.StatusNoContent},
		{name: "authenticated", header: "Bearer " + accessToken, wantStatus: http.StatusNoContent, wantUserID: userID},
		{name: "invalid credentials", header: "Bearer invalid", wantStatus: http.StatusUnauthorized},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			engine := gin.New()
			engine.Use(OptionalAuth(tokens, authSessionValidator{
				active: true, userID: userID, sessionID: sessionID,
			}))
			engine.GET("/public", func(c *gin.Context) {
				actual, ok := c.Get(AuthenticatedUserIDKey)
				if testCase.wantUserID != uuid.Nil && (!ok || actual != testCase.wantUserID) {
					c.Status(http.StatusInternalServerError)
					return
				}
				if testCase.wantUserID == uuid.Nil && ok {
					c.Status(http.StatusInternalServerError)
					return
				}
				c.Status(http.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodGet, "/public", nil)
			if testCase.header != "" {
				request.Header.Set("Authorization", testCase.header)
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, testCase.wantStatus, response.Body.String())
			}
		})
	}
}

func TestRequireAuthChecksSessionState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	sessionID := uuid.New()
	tokens := utils.NewTokenManager("0123456789abcdef0123456789abcdef", "test-issuer", time.Minute)
	accessToken, _, err := tokens.Issue(userID, sessionID)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	for _, testCase := range []struct {
		name       string
		active     bool
		wantStatus int
	}{
		{name: "active session", active: true, wantStatus: http.StatusNoContent},
		{name: "revoked session", active: false, wantStatus: http.StatusUnauthorized},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			engine := gin.New()
			engine.Use(RequireAuth(tokens, authSessionValidator{
				active:    testCase.active,
				userID:    userID,
				sessionID: sessionID,
			}))
			engine.GET("/protected", func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.Header.Set("Authorization", "Bearer "+accessToken)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, testCase.wantStatus, response.Body.String())
			}
		})
	}
}
