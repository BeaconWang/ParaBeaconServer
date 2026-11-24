package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/pkg/utils"
)

type AccountLookup interface {
	FindByID(context.Context, uuid.UUID) (*model.User, error)
}

func RequireBeaconAccount(tokens *utils.TokenManager, accounts AccountLookup, validators ...ActiveSessionValidator) gin.HandlerFunc {
	return func(c *gin.Context) {
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
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "release management is not configured"})
			return
		}
		account, err := accounts.FindByID(c.Request.Context(), userID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "account authorization temporarily unavailable"})
			return
		}
		if account == nil || !strings.EqualFold(strings.TrimSpace(account.Username), "beacon") {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "release management requires the Beacon account"})
			return
		}
		c.Next()
	}
}
