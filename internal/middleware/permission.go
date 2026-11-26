package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/pkg/utils"
)

const PermissionKey = "parabeacon.permissions"

type PermissionLookup interface {
	HasPermission(context.Context, uuid.UUID, string) (bool, error)
}

func RequirePermission(tokens *utils.TokenManager, permissions PermissionLookup, permission string, validators ...ActiveSessionValidator) gin.HandlerFunc {
	permission = strings.TrimSpace(permission)
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
		if permissions == nil || permission == "" {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "authorization is not configured"})
			return
		}
		allowed, err := permissions.HasPermission(c.Request.Context(), userID, permission)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "authorization temporarily unavailable"})
			return
		}
		if !allowed {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			return
		}
		c.Set(PermissionKey, permission)
		c.Next()
	}
}
