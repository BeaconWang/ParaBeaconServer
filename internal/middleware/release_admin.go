package middleware

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/pkg/utils"
)

func RequireReleaseAdmin(configuredToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if len(configuredToken) < 32 {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "release publishing is not configured"})
			return
		}
		if !matchesReleaseAdminToken(configuredToken, c.GetHeader("X-Release-Admin-Token")) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

type AccountLookup interface {
	FindByID(context.Context, uuid.UUID) (*model.User, error)
}

func RequireReleaseAdminOrBeaconAccount(configuredToken string, tokens *utils.TokenManager, accounts AccountLookup, validators ...ActiveSessionValidator) gin.HandlerFunc {
	return func(c *gin.Context) {
		if len(configuredToken) >= 32 && matchesReleaseAdminToken(configuredToken, c.GetHeader("X-Release-Admin-Token")) {
			c.Next()
			return
		}
		userID, err := authenticateUser(c, tokens, validators...)
		if err != nil {
			if errors.Is(err, errAuthenticationService) {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "authentication temporarily unavailable"})
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		if accounts == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "release publishing is not configured"})
			return
		}
		account, err := accounts.FindByID(c.Request.Context(), userID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "account authorization temporarily unavailable"})
			return
		}
		if account == nil || !strings.EqualFold(strings.TrimSpace(account.Username), "beacon") {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "release publishing requires the Beacon account"})
			return
		}
		c.Next()
	}
}

func matchesReleaseAdminToken(configuredToken, provided string) bool {
	return len(provided) == len(configuredToken) && subtle.ConstantTimeCompare([]byte(provided), []byte(configuredToken)) == 1
}
