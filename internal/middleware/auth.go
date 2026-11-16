package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/pkg/utils"
)

const AuthenticatedUserIDKey = "authenticated_user_id"

type ActiveSessionValidator interface {
	IsActiveSession(ctx context.Context, sessionID, userID uuid.UUID, now time.Time) (bool, error)
}

func RequireAuth(tokens *utils.TokenManager, validators ...ActiveSessionValidator) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		parts := strings.Fields(header)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		claims, err := tokens.Parse(parts[1])
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		userID, err := uuid.Parse(claims.Subject)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		if claims.SessionID != "" {
			if len(validators) == 0 {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
				return
			}
			sessionID, _ := uuid.Parse(claims.SessionID)
			active, err := validators[0].IsActiveSession(c.Request.Context(), sessionID, userID, time.Now().UTC())
			if err != nil {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "authentication temporarily unavailable"})
				return
			}
			if !active {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
				return
			}
		}
		c.Set(AuthenticatedUserIDKey, userID)
		c.Next()
	}
}
