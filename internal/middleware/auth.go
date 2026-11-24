package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/pkg/utils"
)

const AuthenticatedUserIDKey = "authenticated_user_id"

var (
	errUnauthorized          = errors.New("unauthorized")
	errAuthenticationService = errors.New("authentication service unavailable")
)

type ActiveSessionValidator interface {
	IsActiveSession(ctx context.Context, sessionID, userID uuid.UUID, now time.Time) (bool, error)
}

func authenticateUser(c *gin.Context, tokens *utils.TokenManager, validators ...ActiveSessionValidator) (uuid.UUID, error) {
	if tokens == nil {
		return uuid.Nil, errAuthenticationService
	}
	header := c.GetHeader("Authorization")
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return uuid.Nil, errUnauthorized
	}
	claims, err := tokens.Parse(parts[1])
	if err != nil {
		return uuid.Nil, errUnauthorized
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, errUnauthorized
	}
	if claims.SessionID != "" {
		if len(validators) == 0 {
			return uuid.Nil, errUnauthorized
		}
		sessionID, err := uuid.Parse(claims.SessionID)
		if err != nil {
			return uuid.Nil, errUnauthorized
		}
		active, err := validators[0].IsActiveSession(c.Request.Context(), sessionID, userID, time.Now().UTC())
		if err != nil {
			return uuid.Nil, errAuthenticationService
		}
		if !active {
			return uuid.Nil, errUnauthorized
		}
	}
	c.Set(AuthenticatedUserIDKey, userID)
	return userID, nil
}

func OptionalAuth(tokens *utils.TokenManager, validators ...ActiveSessionValidator) gin.HandlerFunc {
	requireAuth := RequireAuth(tokens, validators...)
	return func(c *gin.Context) {
		if strings.TrimSpace(c.GetHeader("Authorization")) == "" {
			c.Next()
			return
		}
		requireAuth(c)
	}
}

func RequireAuth(tokens *utils.TokenManager, validators ...ActiveSessionValidator) gin.HandlerFunc {
	return func(c *gin.Context) {
		_, err := authenticateUser(c, tokens, validators...)
		if err != nil {
			if errors.Is(err, errAuthenticationService) {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "authentication temporarily unavailable"})
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}
